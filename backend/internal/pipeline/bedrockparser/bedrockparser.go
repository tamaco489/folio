// Package bedrockparser は経路 B のページ単位の抽出 State のロジックを担う
//
// Step Functions の Map から 1 ページ = 1 起動で並列に呼ばれるため、責務をページ画像 1 枚の取得・抽出・保存に限る
// 言語判定・ページ数の判定・S3 の一覧取得・ページ結果の結合は持たない (Map の中に置くと並列度の制御と責務分離が崩れる)
// 出力は S3 キーだけとし、実体は S3 に置く (State 間の上限 256KB)
package bedrockparser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/bedrockroute"
)

var (
	// ErrEmptyJobID は入力に jobId が含まれないことを示す
	ErrEmptyJobID = errors.New("bedrockparser: job id is empty")

	// ErrInvalidPage はページ番号が 1 未満であることを示す
	ErrInvalidPage = errors.New("bedrockparser: page number must be 1 or greater")
)

// InvalidInputError は Map の 1 反復から渡された入力では処理を進められないことを表す
//
// Go の Lambda はエラーの型名を errorType として Step Functions に報告するため、再試行しても解消しない失敗を Retry から除外できるよう専用の型にする
// 存在しないページ画像 (s3.ErrNotFound) と空のページ画像もここに含める — 前処理 State がページ画像を書き終えてから Map が始まるため、無いものは再試行しても現れない
type InvalidInputError struct {
	Err error
}

func (e *InvalidInputError) Error() string { return e.Err.Error() }

func (e *InvalidInputError) Unwrap() error { return e.Err }

// PageDecodeError はモデルの応答をページ結果として解釈できなかったことを表す
//
// 同じ画像を送り直しても解消する見込みが薄いため、InvalidInputError と同じく Retry から除外できる専用の型にする (bedrock.ErrInvalidJSON と bedrockroute.ErrPageDecode を包む)
type PageDecodeError struct {
	Err error
}

func (e *PageDecodeError) Error() string { return e.Err.Error() }

func (e *PageDecodeError) Unwrap() error { return e.Err }

// Input は Map の 1 反復から渡される 1 ページ分の指定
//
// ページ画像のキーは受け取らず jobId とページ番号から導出する (前処理 State がキーを列挙せず pageImagePrefix と pageCount で表す設計に合わせる)
type Input struct {
	JobID    string          `json:"jobId"`
	Page     int             `json:"page"`     // Page は 1 始まりのページ番号
	Language domain.Language `json:"language"` // Language は言語ヒント (空なら指定しない)
}

// Output は Map の 1 反復の結果
//
// State 間の 256KB 上限に収めるため、結果の実体は S3 に置きキーだけを返す
type Output struct {
	JobID     string `json:"jobId"`
	Page      int    `json:"page"`
	ResultKey string `json:"resultKey"`
}

// PageOutput は S3 に保存する 1 ページ分の封筒
//
// 後段が所要時間とコストを集計できるよう、抽出結果に時刻と所要時間を添える
type PageOutput struct {
	JobID       string                  `json:"jobId"`
	Page        int                     `json:"page"`
	ExtractedAt time.Time               `json:"extractedAt"` // ExtractedAt は保存直前の時刻
	DurationMs  int64                   `json:"durationMs"`  // DurationMs は画像の取得から抽出完了までの所要時間
	Result      bedrockroute.PageResult `json:"result"`
}

// Handler は 1 ページ分の抽出の依存をまとめる
type Handler struct {
	storage   *s3.Client
	extractor *bedrockroute.Extractor
	now       func() time.Time
}

// Option は Handler の設定を変更する
type Option func(*Handler)

// WithClock は時刻の取得方法を差し替える (テストで固定時刻を与えるために使う)
func WithClock(now func() time.Time) Option {
	return func(h *Handler) { h.now = now }
}

// New は 1 ページ分の抽出のハンドラを組み立てる
func New(storage *s3.Client, extractor *bedrockroute.Extractor, opts ...Option) *Handler {
	h := &Handler{
		storage:   storage,
		extractor: extractor,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Handle はページ画像 1 枚を取得して構造化し、結果の所在を返す
//
// スロットリングのリトライは awsx/bedrock が担うため、ここでは再送しない
func (h *Handler) Handle(ctx context.Context, in Input) (Output, error) {
	if in.JobID == "" {
		return Output{}, &InvalidInputError{Err: ErrEmptyJobID}
	}
	if in.Page < 1 {
		return Output{}, &InvalidInputError{Err: fmt.Errorf("%w: %d", ErrInvalidPage, in.Page)}
	}

	started := h.now()
	image, err := h.storage.GetBytes(ctx, s3.PageImageKey(in.JobID, in.Page))
	if err != nil {
		if errors.Is(err, s3.ErrNotFound) {
			return Output{}, &InvalidInputError{Err: err}
		}
		return Output{}, err
	}

	// RecordKey は本番では使わないため空のまま渡す (再生テストは Converser の層でキーを補う)
	result, err := h.extractor.ExtractPage(ctx, bedrockroute.PageInput{
		Page:     in.Page,
		Image:    image,
		Language: in.Language,
	})
	if err != nil {
		return Output{}, classify(err)
	}
	finished := h.now()

	key := s3.BedrockPageResultKey(in.JobID, in.Page)
	if err := h.storage.PutJSON(ctx, key, PageOutput{
		JobID:       in.JobID,
		Page:        in.Page,
		ExtractedAt: finished.UTC(),
		DurationMs:  finished.Sub(started).Milliseconds(),
		Result:      *result,
	}); err != nil {
		return Output{}, err
	}

	return Output{JobID: in.JobID, Page: in.Page, ResultKey: key}, nil
}

// classify は抽出の失敗のうち再試行しても解消しないものを専用の型へ写す
func classify(err error) error {
	switch {
	case errors.Is(err, bedrockroute.ErrPageDecode):
		return &PageDecodeError{Err: err}
	case errors.Is(err, bedrockroute.ErrEmptyImage):
		return &InvalidInputError{Err: err}
	}
	return err
}
