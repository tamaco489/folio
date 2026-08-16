// Package finalize は Stage 3 の出口の Lambda (finalizer) のロジックを担う
//
// Parallel で並走した両経路の結果を S3 の規定のキーから読み、正規化 (normalize) と検証 (verify) を通して outputs/ へ永続化し、レビュー要否を決めて DynamoDB の状態を更新する
// 片方の経路だけが失敗しても成功した経路の結果は保持し、失敗の理由は comparison.json に残す (両経路の比較が目的である以上、片方でも得られる情報はある)
// 出力は S3 キーと小さな判定結果だけとし、実体は含めない (State 間の上限 256KB)
package finalize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify"
)

var (
	// ErrEmptyJobID は入力に jobId が含まれないことを示す
	ErrEmptyJobID = errors.New("finalize: job id is empty")

	// ErrInvalidPageCount はページ数が 1 未満であることを示す
	ErrInvalidPageCount = errors.New("finalize: page count must be 1 or greater")
)

// InvalidInputError は Step Functions から渡された入力では処理を進められないことを表す
//
// Go の Lambda はエラーの型名を errorType として Step Functions に報告するため、再試行しても解消しない失敗を Retry から除外できるよう専用の型にする
type InvalidInputError struct {
	Err error
}

func (e *InvalidInputError) Error() string { return e.Err.Error() }

func (e *InvalidInputError) Unwrap() error { return e.Err }

// NoResultError は両経路とも結果を残せず、正規化・検証する材料が無いことを表す
//
// 経路の失敗は Parallel の中で確定しており finalizer を再試行しても解消しないため、InvalidInputError と同じく型名で Retry から除外できるようにする
// comparison.json には経路ごとの理由を書き終えており、Handle は返す前に DynamoDB を FAILED に更新する
type NoResultError struct {
	JobID  string
	Reason string // Reason は経路ごとの失敗理由をまとめたもの (DynamoDB の errorReason にも同じ値を書く)
}

func (e *NoResultError) Error() string {
	return fmt.Sprintf("finalize: no route produced a result for job %s: %s", e.JobID, e.Reason)
}

// Input は Step Functions が Parallel の後に渡す入力
//
// jobId pageCount hasTextLayer language skipTextract は前処理 (preprocess.Output) の値をそのまま渡す前提
// 経路の成果物は Parallel の出力からではなく S3 の規定のキーから読むため、Parallel の出力形 (配列か名前付きか、Catch の置き場所) に依存しない
// textract / bedrock は各ブランチの Catch の結果を置く場所で、失敗の理由 (Error / Cause) を comparison.json に残すためだけに使う (成功時の出力が入っていても無視する)
type Input struct {
	JobID        string          `json:"jobId"`
	PageCount    int             `json:"pageCount"`
	HasTextLayer bool            `json:"hasTextLayer"`
	Language     domain.Language `json:"language"`
	SkipTextract bool            `json:"skipTextract"`
	Textract     json.RawMessage `json:"textract,omitempty"`
	Bedrock      json.RawMessage `json:"bedrock,omitempty"`
}

// Output は Choice に渡す判定結果と成果物の所在
//
// Choice は needsReview で振り分けるだけでよい (閾値の判定は verify に閉じ、二重管理にしない)
type Output struct {
	JobID             string        `json:"jobId"`
	Status            dynamo.Status `json:"status"`
	NeedsReview       bool          `json:"needsReview"`
	ResultTextractKey string        `json:"resultTextractKey,omitempty"`
	ResultBedrockKey  string        `json:"resultBedrockKey,omitempty"`
	ComparisonKey     string        `json:"comparisonKey"`
}

// Handler は正規化・検証・永続化・状態更新の依存をまとめる
type Handler struct {
	docs     *s3.Client
	jobs     *dynamo.Client
	verifier *verify.Verifier
	now      func() time.Time
}

// Option は Handler の設定を変更する
type Option func(*Handler)

// WithClock は時刻の取得方法を差し替える (テストで finalizedAt と経路 B の時刻計算を決定的にするために使う)
func WithClock(now func() time.Time) Option {
	return func(h *Handler) { h.now = now }
}

// New はハンドラを組み立てる
//
// verifier は両経路で共有する (Crossref のレート制限は crossref.Client が直列化する)
func New(docs *s3.Client, jobs *dynamo.Client, verifier *verify.Verifier, opts ...Option) *Handler {
	h := &Handler{
		docs:     docs,
		jobs:     jobs,
		verifier: verifier,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Handle は両経路の結果を正規化・検証して永続化し、レビュー要否と DynamoDB の状態を決める
//
// 入力不正 (InvalidInputError) は jobId が無いこともあるため DynamoDB を触らない
// それ以外の失敗は MarkFailed を試みてから返す (MarkFailed 自体の失敗はログに残すだけで元のエラーを優先する)
func (h *Handler) Handle(ctx context.Context, in Input) (Output, error) {
	if in.JobID == "" {
		return Output{}, &InvalidInputError{Err: ErrEmptyJobID}
	}
	if in.PageCount < 1 {
		return Output{}, &InvalidInputError{Err: fmt.Errorf("%w: %d", ErrInvalidPageCount, in.PageCount)}
	}

	out, err := h.finalize(ctx, in)
	if err != nil {
		reason := err.Error()
		if noResult, ok := errors.AsType[*NoResultError](err); ok {
			reason = noResult.Reason
		}
		if _, merr := h.jobs.MarkFailed(ctx, in.JobID, reason); merr != nil {
			slog.ErrorContext(ctx, "failed to mark job as failed", "jobId", in.JobID, "error", merr, "cause", err)
		}
		return Output{}, err
	}
	return out, nil
}

// finalize は経路 A → 経路 B の順に処理し、成果物を result-*.json → comparison.json → DynamoDB の順に書く
//
// 再実行はすべて上書きであるため冪等になる (経路 A の入力は work/ の正規化前の結果であり、自分の出力を読み戻すことはない)
func (h *Handler) finalize(ctx context.Context, in Input) (Output, error) {
	layer, err := h.textLayer(ctx, in)
	if err != nil {
		return Output{}, err
	}

	textract, err := h.textractRoute(ctx, in, layer)
	if err != nil {
		return Output{}, err
	}
	bedrock, err := h.bedrockRoute(ctx, in, layer)
	if err != nil {
		return Output{}, err
	}

	routes := Routes{Textract: textract.result, Bedrock: bedrock.result}
	succeeded, failed := 0, 0
	for _, r := range []RouteResult{routes.Textract, routes.Bedrock} {
		switch r.Status {
		case RouteSucceeded:
			succeeded++
		case RouteFailed:
			failed++
		}
	}
	// 片方の経路が失敗した事実そのものがレビューに値する (skipped は設計どおり通していないだけなので、成功した経路の判定に従う)
	needsReview := succeeded > 0 && failed > 0
	for _, r := range []RouteResult{routes.Textract, routes.Bedrock} {
		if r.Status == RouteSucceeded && r.NeedsReview {
			needsReview = true
		}
	}
	status := dynamo.StatusCompleted
	switch {
	case succeeded == 0:
		status = dynamo.StatusFailed
	case needsReview:
		status = dynamo.StatusReviewPending
	}

	cmp := Comparison{
		JobID:       in.JobID,
		FinalizedAt: h.now().UTC(),
		Status:      status,
		NeedsReview: needsReview,
		Routes:      routes,
	}
	if succeeded == 2 {
		d := diff(*textract.doc, *bedrock.doc)
		cmp.Diff = &d
	}
	comparisonKey := s3.ComparisonKey(in.JobID)
	if err := h.docs.PutJSON(ctx, comparisonKey, cmp); err != nil {
		return Output{}, err
	}

	if succeeded == 0 {
		return Output{}, &NoResultError{JobID: in.JobID, Reason: failureReason(routes)}
	}
	if _, err := h.jobs.UpdateStatus(ctx, in.JobID, status); err != nil {
		return Output{}, err
	}
	return Output{
		JobID:             in.JobID,
		Status:            status,
		NeedsReview:       needsReview,
		ResultTextractKey: routes.Textract.ResultKey,
		ResultBedrockKey:  routes.Bedrock.ResultKey,
		ComparisonKey:     comparisonKey,
	}, nil
}

// textLayer は原本のテキストレイヤーを読む
//
// 前処理が「無い」と判定した文書は読みに行かない
// あるはずのものが S3 に無ければ空文字で続行し、verify が突合の省略を警告に残す
func (h *Handler) textLayer(ctx context.Context, in Input) (string, error) {
	if !in.HasTextLayer {
		return "", nil
	}
	b, err := h.docs.GetBytes(ctx, s3.TextLayerKey(in.JobID))
	if err != nil {
		if errors.Is(err, s3.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// failureReason は両経路の失敗理由を DynamoDB の errorReason 向けに 1 行へまとめる
func failureReason(routes Routes) string {
	return fmt.Sprintf("textract: %s; bedrock: %s", describe(routes.Textract), describe(routes.Bedrock))
}

func describe(r RouteResult) string {
	if r.Status != RouteFailed {
		return string(r.Status)
	}
	if r.Cause == "" {
		return r.Error
	}
	return r.Error + ": " + r.Cause
}
