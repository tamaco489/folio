package textractparser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-lambda-go/events"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/sfn"
	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/textractroute"
)

// StartInput は Step Functions が waitForTaskToken で渡す起動の入力
//
// language pageCount hasTextLayer は前処理 (preprocess.Output) の値をそのまま渡す前提で、domain.Source を組み立てるためだけに使う
type StartInput struct {
	JobID        string          `json:"jobId"`
	TaskToken    string          `json:"taskToken"`
	Language     domain.Language `json:"language"`
	PageCount    int             `json:"pageCount"`
	HasTextLayer bool            `json:"hasTextLayer"`
}

// StartOutput は起動の戻り値
//
// Step Functions はトークンで再開するためこの値を使わない。ログと手動調査のためにだけ返す
type StartOutput struct {
	JobID         string `json:"jobId"`
	TextractJobID string `json:"textractJobId"`
	CallbackKey   string `json:"callbackKey"`
}

// Callback は起動側が S3 (s3.TextractCallbackKey) へ退避し、完了通知を受けた側が読み戻す情報
//
// タスクトークンは Textract の JobTag (64 文字上限) に収まらないため、JobTag には jobId だけを入れてトークンは S3 に置く
type Callback struct {
	JobID         string                         `json:"jobId"`
	TaskToken     string                         `json:"taskToken"`
	TextractJobID string                         `json:"textractJobId"` // TextractJobID は Retry で再起動された古いジョブの通知を見分けるために持つ
	StartedAt     time.Time                      `json:"startedAt"`
	FeatureTypes  []awstextracttypes.FeatureType `json:"featureTypes"`
	Source        domain.Source                  `json:"source"`
}

// Result は SendTaskSuccess で Step Functions へ返す Task の出力
//
// ResultKey は正規化前の中間結果 (s3.TextractDocumentKey) であり、finalizer がこれを読んで outputs/ へ最終成果物を書く
type Result struct {
	JobID     string `json:"jobId"`
	ResultKey string `json:"resultKey"`
	RawKey    string `json:"rawKey"`
}

// FailureCause は SendTaskFailure の Cause に JSON 文字列として入れる失敗の理由
type FailureCause struct {
	JobID         string `json:"jobId"`
	TextractJobID string `json:"textractJobId"`
	Message       string `json:"message"`
}

// Analysis は Textract の非同期解析の起動に用いる設定
type Analysis struct {
	SNSTopicARN  string                         // SNSTopicARN は Textract が完了通知を発行するトピック
	RoleARN      string                         // RoleARN は Textract がトピックへ発行するために引き受けるロール
	FeatureTypes []awstextracttypes.FeatureType // FeatureTypes は解析に用いる機能 (provenance.cost にもそのまま記録する)
}

// Handler は起動と完了通知の両方を担う
type Handler struct {
	docs      *s3.Client
	analyzer  *textract.Client
	states    *sfn.Client
	extractor *textractroute.Extractor
	analysis  Analysis
	now       func() time.Time
}

// Option は Handler の設定を変更する
type Option func(*Handler)

// WithClock は現在時刻の取得を差し替える (テストで extractedAt と durationMs を決定的にするために用いる)
func WithClock(now func() time.Time) Option {
	return func(h *Handler) {
		if now != nil {
			h.now = now
		}
	}
}

// New はハンドラを組み立てる
func New(docs *s3.Client, analyzer *textract.Client, states *sfn.Client, extractor *textractroute.Extractor, analysis Analysis, opts ...Option) *Handler {
	h := &Handler{
		docs:      docs,
		analyzer:  analyzer,
		states:    states,
		extractor: extractor,
		analysis:  analysis,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Handle は入力の形でイベントの種類を判別して振り分ける
//
// SNS からの配送は Records[].EventSource が "aws:sns" の events.SNSEvent で届き、それ以外は Step Functions からの起動として扱う
// 完了通知の経路は Step Functions へタスクトークンで応答済みで Lambda の戻り値に載せる出力が無いため、nil, nil で正常終了する
// 完了通知の処理で返すエラーは Step Functions には届かず、Lambda の非同期呼び出しの再試行を招くだけであるため、型で分類するのは起動のエラーだけとする
func (h *Handler) Handle(ctx context.Context, raw json.RawMessage) (*StartOutput, error) {
	if ev, ok := asSNSEvent(raw); ok {
		for _, rec := range ev.Records {
			if err := h.callback(ctx, []byte(rec.SNS.Message)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}

	var in StartInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, &PermanentError{Err: fmt.Errorf("decode start input: %w", err)}
	}
	out, err := h.start(ctx, in)
	if err != nil {
		return nil, classify(err)
	}
	return out, nil
}

// asSNSEvent は入力が SNS からの配送かを判定する
func asSNSEvent(raw json.RawMessage) (*events.SNSEvent, bool) {
	var ev events.SNSEvent
	if err := json.Unmarshal(raw, &ev); err != nil || len(ev.Records) == 0 {
		return nil, false
	}
	for _, rec := range ev.Records {
		if rec.EventSource != snsEventSource {
			return nil, false
		}
	}
	return &ev, true
}

// start は Textract を開始し、完了通知の処理に要する情報を S3 へ退避する
//
// Textract の開始より先に退避すると通知の JobId と突き合わせられないため、退避は開始の後に行う
// 退避に失敗した場合は Lambda のエラーとして返し、Step Functions の Retry に委ねる (開始済みのジョブは孤児になり、その通知は退避が無いか JobId が食い違うため無視される)
func (h *Handler) start(ctx context.Context, in StartInput) (*StartOutput, error) {
	if in.JobID == "" {
		return nil, ErrEmptyJobID
	}
	if in.TaskToken == "" {
		return nil, ErrEmptyTaskToken
	}

	key := s3.OriginalPDFKey(in.JobID)
	info, err := h.docs.Head(ctx, key)
	if err != nil {
		return nil, err
	}

	startedAt := h.now()
	textractJobID, err := h.analyzer.StartDocumentAnalysis(ctx, textract.StartAnalysisInput{
		Document:     textract.S3Location{Bucket: h.docs.Bucket(), Key: key},
		FeatureTypes: h.analysis.FeatureTypes,
		SNSTopicARN:  h.analysis.SNSTopicARN,
		RoleARN:      h.analysis.RoleARN,
		JobTag:       in.JobID,
	})
	if err != nil {
		return nil, err
	}

	cb := Callback{
		JobID:         in.JobID,
		TaskToken:     in.TaskToken,
		TextractJobID: textractJobID,
		StartedAt:     startedAt,
		FeatureTypes:  h.analysis.FeatureTypes,
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
	}
	callbackKey := s3.TextractCallbackKey(in.JobID)
	if err := h.docs.PutJSON(ctx, callbackKey, cb); err != nil {
		return nil, err
	}

	return &StartOutput{JobID: in.JobID, TextractJobID: textractJobID, CallbackKey: callbackKey}, nil
}

// callback は Textract の完了通知 1 件を処理し、Step Functions へ応答する
//
// 退避した情報が無い場合はエラーを返して Lambda の非同期呼び出しの再試行に委ねる (起動側の退避が通知より遅れる可能性に備える)
// 退避した情報の JobId と食い違う場合は Retry で再起動される前の古いジョブの通知として無視する
func (h *Handler) callback(ctx context.Context, message []byte) error {
	n, err := textract.ParseCompletionNotification(message)
	if err != nil {
		return err
	}
	if n.JobTag == "" {
		return fmt.Errorf("%w: textract job %s", ErrEmptyJobTag, n.JobID)
	}

	var cb Callback
	if err := h.docs.GetJSON(ctx, s3.TextractCallbackKey(n.JobTag), &cb); err != nil {
		return fmt.Errorf("textractparser: read callback of job %s: %w", n.JobTag, err)
	}
	if cb.TextractJobID != n.JobID {
		slog.InfoContext(
			ctx, "ignored completion notification of a stale textract job",
			"jobId", cb.JobID,
			"textractJobId", n.JobID,
			"currentTextractJobId", cb.TextractJobID,
		)
		return nil
	}

	if !n.Succeeded() {
		return h.fail(ctx, cb, FailureTextractJob, fmt.Sprintf("textract job ended with status %s", n.Status))
	}

	result, err := h.process(ctx, cb)
	if err != nil {
		return h.fail(ctx, cb, FailureExtract, err.Error())
	}
	return h.respond(ctx, cb, h.states.SendTaskSuccess(ctx, cb.TaskToken, result))
}

// process は Textract の結果を取得して構造化し、生出力と正規化前の結果を S3 (work/) へ保存する
//
// outputs/ の最終成果物は finalizer だけが書く
func (h *Handler) process(ctx context.Context, cb Callback) (Result, error) {
	analysis, err := h.analyzer.GetDocumentAnalysis(ctx, cb.TextractJobID)
	if err != nil {
		return Result{}, err
	}

	// 生出力は構造化より先に保存する (Bedrock が失敗しても課金済みの Textract 出力を残す)
	rawKey := s3.TextractRawKey(cb.JobID)
	if err := h.docs.PutJSON(ctx, rawKey, analysis); err != nil {
		return Result{}, err
	}

	// PaperID は Bedrock の記録・再生キーにしか使われず、本番の Client では参照されないため jobId を渡す
	doc, err := h.extractor.Extract(ctx, textractroute.Input{
		JobID:        cb.JobID,
		PaperID:      cb.JobID,
		Source:       cb.Source,
		Analysis:     analysis,
		FeatureTypes: cb.FeatureTypes,
	})
	if err != nil {
		return Result{}, err
	}

	// 出力トークン数が上限 (maxTokens) にどれだけ近いかを CloudWatch Logs で追えるようにする
	slog.InfoContext(
		ctx, "structured textract output with bedrock",
		"jobId", cb.JobID,
		"inputTokens", doc.Provenance.Cost.BedrockInputTokens,
		"outputTokens", doc.Provenance.Cost.BedrockOutputTokens,
	)

	// 経過時間は Textract の開始から結果の保存直前までとし、Extract はゼロのまま返すためここで埋める
	finished := h.now()
	doc.Provenance.ExtractedAt = finished
	doc.Provenance.DurationMs = finished.Sub(cb.StartedAt).Milliseconds()

	resultKey := s3.TextractDocumentKey(cb.JobID)
	if err := h.docs.PutJSON(ctx, resultKey, doc); err != nil {
		return Result{}, err
	}
	return Result{JobID: cb.JobID, ResultKey: resultKey, RawKey: rawKey}, nil
}

// fail は失敗の理由を構造化して SendTaskFailure で返す
func (h *Handler) fail(ctx context.Context, cb Callback, code, message string) error {
	// SendTaskFailure の Cause は Step Functions の実行履歴にしか残らないため、CloudWatch Logs にも同じ理由を出す
	slog.ErrorContext(
		ctx, "textract task failed",
		"jobId", cb.JobID,
		"textractJobId", cb.TextractJobID,
		"code", code,
		"message", message,
	)

	cause, err := json.Marshal(FailureCause{JobID: cb.JobID, TextractJobID: cb.TextractJobID, Message: message})
	if err != nil {
		return fmt.Errorf("textractparser: encode failure cause: %w", err)
	}
	return h.respond(ctx, cb, h.states.SendTaskFailure(ctx, cb.TaskToken, code, string(cause)))
}

// respond は応答の結果を評価する
//
// トークンがもう有効でない場合 (Task のタイムアウト後など) は再試行しても届かないため、記録だけして正常終了にする
func (h *Handler) respond(ctx context.Context, cb Callback, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sfn.ErrTaskGone) {
		slog.WarnContext(
			ctx, "task is no longer waiting, response dropped",
			"jobId", cb.JobID,
			"error", err,
		)
		return nil
	}
	return err
}
