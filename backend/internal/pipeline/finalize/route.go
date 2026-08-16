package finalize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/bedrockparser"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/bedrockroute"
	"github.com/tamaco489/folio/backend/internal/pipeline/normalize"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify"
)

// route は 1 経路の処理結果
type route struct {
	result RouteResult
	doc    *domain.Document // doc は正規化・検証後の文書で Diff の材料 (succeeded のときだけ)
}

// textractRoute は経路 A の結果を読み、正規化・検証して outputs/ へ書く
//
// 成功したかは textract-parser が書く正規化前の結果 (s3.TextractDocumentKey) の有無で判定する
// SkipTextract の文書は経路を通っていないため S3 を読みに行かない
func (h *Handler) textractRoute(ctx context.Context, in Input, layer string) (route, error) {
	if in.SkipTextract {
		return route{result: RouteResult{Status: RouteSkipped}}, nil
	}

	key := s3.TextractDocumentKey(in.JobID)
	var doc domain.Document
	if err := h.docs.GetJSON(ctx, key, &doc); err != nil {
		if errors.Is(err, s3.ErrNotFound) {
			return route{result: failedResult(in.Textract, fmt.Sprintf("%s が無い", key))}, nil
		}
		return route{}, err
	}
	return h.finish(ctx, doc, layer, s3.ResultTextractKey(in.JobID))
}

// bedrockRoute は経路 B のページ結果を結合し、正規化・検証して outputs/ へ書く
//
// 成功したかはページ結果が 1 ページ以上読めたかで判定し、欠落したページは Merge が警告に落として続行する
func (h *Handler) bedrockRoute(ctx context.Context, in Input, layer string) (route, error) {
	pages, err := h.readPages(ctx, in)
	if err != nil {
		return route{}, err
	}
	if len(pages) == 0 {
		prefix := path.Dir(s3.BedrockPageResultKey(in.JobID, 1)) + "/"
		return route{result: failedResult(in.Bedrock, fmt.Sprintf("%s 配下にページ結果が 1 件も無い", prefix))}, nil
	}

	key := s3.OriginalPDFKey(in.JobID)
	info, err := h.docs.Head(ctx, key)
	if err != nil {
		return route{}, err
	}
	results := make([]bedrockroute.PageResult, 0, len(pages))
	for _, p := range pages {
		results = append(results, p.Result)
	}
	// Source は textract-parser の起動時と同じ組み立て方にし、両経路の source を一致させる
	doc := bedrockroute.Merge(bedrockroute.MergeInput{
		JobID: in.JobID,
		Source: domain.Source{
			Bucket: h.docs.Bucket(),
			Key:    key,
			// 元のファイル名は受領時に失われているため validator が DynamoDB に残す filename と同じく S3 キーを入れる
			Filename: key,
			// バリデーションがキーの jobId と PDF の SHA-256 の一致を確認済みであるため再計算しない
			SHA256:       in.JobID,
			Language:     in.Language,
			PageCount:    in.PageCount,
			HasTextLayer: in.HasTextLayer,
			UploadedAt:   info.LastModified,
		},
		Pages: results,
	})
	doc.Provenance.ExtractedAt, doc.Provenance.DurationMs = timing(pages)
	return h.finish(ctx, doc, layer, s3.ResultBedrockKey(in.JobID))
}

// readPages は経路 B のページ結果を 1 ページ目から pageCount まで読み、無いページは飛ばす
//
// Map の一部が失敗したページは無いままであり、再試行しても現れないため NotFound は失敗として扱わない
func (h *Handler) readPages(ctx context.Context, in Input) ([]bedrockparser.PageOutput, error) {
	pages := make([]bedrockparser.PageOutput, 0, in.PageCount)
	for page := 1; page <= in.PageCount; page++ {
		var out bedrockparser.PageOutput
		if err := h.docs.GetJSON(ctx, s3.BedrockPageResultKey(in.JobID, page), &out); err != nil {
			if errors.Is(err, s3.ErrNotFound) {
				continue
			}
			return nil, err
		}
		pages = append(pages, out)
	}
	return pages, nil
}

// timing はページ結果から経路 B の抽出時刻と所要時間を求める
//
// ExtractedAt は最も遅いページの完了時刻、DurationMs は最も早いページの開始 (完了時刻 - 所要時間) から最も遅いページの完了までの経過にする
// Map は並列に走るためページごとの所要時間の合計は並列度に左右されて比較に使えず、経路 A の「Textract の開始から結果の保存まで」と同じ壁時計時間で揃える
// 完了時刻が無いページは計算から外す (normalize が extractedAt の未設定を警告する)
func timing(pages []bedrockparser.PageOutput) (time.Time, int64) {
	var started, finished time.Time
	for _, p := range pages {
		if p.ExtractedAt.IsZero() {
			continue
		}
		s := p.ExtractedAt.Add(-time.Duration(p.DurationMs) * time.Millisecond)
		if started.IsZero() || s.Before(started) {
			started = s
		}
		if p.ExtractedAt.After(finished) {
			finished = p.ExtractedAt
		}
	}
	if finished.IsZero() {
		return time.Time{}, 0
	}
	return finished.UTC(), finished.Sub(started).Milliseconds()
}

// finish は経路の文書を正規化・検証して outputs/ へ書き、RouteResult に畳む
func (h *Handler) finish(ctx context.Context, doc domain.Document, layer string, key string) (route, error) {
	doc = normalize.Normalize(doc, normalize.WithClock(h.now))
	doc, report := h.verifier.Verify(ctx, doc, verify.Original{Text: layer, HasTextLayer: doc.Source.HasTextLayer})
	if err := h.docs.PutJSON(ctx, key, doc); err != nil {
		return route{}, err
	}

	cost := doc.Provenance.Cost
	return route{
		result: RouteResult{
			Status:      RouteSucceeded,
			ResultKey:   key,
			NeedsReview: report.NeedsReview,
			DurationMs:  doc.Provenance.DurationMs,
			Cost:        &cost,
			Warnings:    doc.Provenance.Warnings,
			Report:      &report,
		},
		doc: &doc,
	}, nil
}

// failedResult は経路の失敗を RouteResult に畳む
//
// 理由は Parallel の Catch が置いた Error / Cause を優先し、無ければ finalizer 側で観測した事実 (結果が S3 に無い) を書く
func failedResult(catch json.RawMessage, fallback string) RouteResult {
	r := RouteResult{Status: RouteFailed}
	if c, ok := parseCatch(catch); ok {
		r.Error, r.Cause = c.Error, c.Cause
		return r
	}
	r.Error, r.Cause = ErrorResultNotFound, fallback
	return r
}
