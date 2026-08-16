// Package textractparser は経路 A の Lambda (textract-parser) のロジックを担う
//
// Textract の非同期解析は起動から完了まで Lambda の実行時間を超えうるため、待ち合わせは Step Functions のコールバックパターン (waitForTaskToken) で行う
// 1 つの Lambda が 2 種類のイベントを受け、種類で分岐する
//   - 起動 (start): Step Functions から呼ばれ、Textract を開始してタスクトークンを S3 へ退避する
//   - 完了通知 (callback): SNS から呼ばれ、結果を取得・構造化して SendTaskSuccess / SendTaskFailure で State Machine を再開させる
//
// 出力は S3 キーと小さな判定結果だけとし、実体は含めない (State 間の上限 256KB)
package textractparser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/sfn"
	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/textractroute"
)

// snsEventSource は SNS が Lambda に配送するイベントの EventSource (aws-lambda-go の events/testdata/sns-event.json に同じ)
const snsEventSource = "aws:sns"

// SendTaskFailure の Error に入れる失敗の種別
//
// State Machine (#30) の Catch と部分失敗の扱い (#24) はこの値で分岐する
const (
	// FailureTextractJob は Textract のジョブ自体が失敗した (文書を解析できなかった) ことを表す
	FailureTextractJob = "TextractJobFailed"

	// FailureExtract は Textract は成功したが、結果の取得・構造化・保存のいずれかが失敗したことを表す
	FailureExtract = "ExtractFailed"
)

var (
	// ErrEmptyJobID は起動の入力に jobId が含まれないことを示す
	ErrEmptyJobID = errors.New("textractparser: job id is empty")

	// ErrEmptyTaskToken は起動の入力に taskToken が含まれないことを示す
	ErrEmptyTaskToken = errors.New("textractparser: task token is empty")

	// ErrEmptyJobTag は完了通知に JobTag (= jobId) が含まれないことを示す
	ErrEmptyJobTag = errors.New("textractparser: completion notification has no job tag")
)

// RetryableError は Textract の一時的な失敗を表す
//
// Lambda はエラーの型名を errorType として Step Functions に見せるため、Retry の ErrorEquals はこの型名 ("RetryableError") で照合する
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string { return "textractparser: retryable: " + e.Err.Error() }

func (e *RetryableError) Unwrap() error { return e.Err }

// PermanentError は RetryableError に該当しない起動の失敗を表す (入力不正、Textract による文書の拒否、S3 の失敗など)
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return "textractparser: " + e.Err.Error() }

func (e *PermanentError) Unwrap() error { return e.Err }

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
	JobID         string              `json:"jobId"`
	TaskToken     string              `json:"taskToken"`
	TextractJobID string              `json:"textractJobId"` // TextractJobID は Retry で再起動された古いジョブの通知を見分けるために持つ
	StartedAt     time.Time           `json:"startedAt"`
	FeatureTypes  []types.FeatureType `json:"featureTypes"`
	Source        domain.Source       `json:"source"`
}

// Result は SendTaskSuccess で Step Functions へ返す Task の出力
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
	SNSTopicARN  string              // SNSTopicARN は Textract が完了通知を発行するトピック
	RoleARN      string              // RoleARN は Textract がトピックへ発行するために引き受けるロール
	FeatureTypes []types.FeatureType // FeatureTypes は解析に用いる機能 (provenance.cost にもそのまま記録する)
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
		slog.InfoContext(ctx, "ignored completion notification of a stale textract job",
			"jobId", cb.JobID, "textractJobId", n.JobID, "currentTextractJobId", cb.TextractJobID)
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

// process は Textract の結果を取得して構造化し、生出力と結果を S3 へ保存する
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

	// 経過時間は Textract の開始から結果の保存直前までとし、Extract はゼロのまま返すためここで埋める
	finished := h.now()
	doc.Provenance.ExtractedAt = finished
	doc.Provenance.DurationMs = finished.Sub(cb.StartedAt).Milliseconds()

	resultKey := s3.ResultTextractKey(cb.JobID)
	if err := h.docs.PutJSON(ctx, resultKey, doc); err != nil {
		return Result{}, err
	}
	return Result{JobID: cb.JobID, ResultKey: resultKey, RawKey: rawKey}, nil
}

// fail は失敗の理由を構造化して SendTaskFailure で返す
func (h *Handler) fail(ctx context.Context, cb Callback, code, message string) error {
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
		slog.WarnContext(ctx, "task is no longer waiting, response dropped", "jobId", cb.JobID, "error", err)
		return nil
	}
	return err
}

// classify は起動のエラーを Step Functions が Retry の対象にできる型とそれ以外に分ける
func classify(err error) error {
	if isTransient(err) {
		return &RetryableError{Err: err}
	}
	return &PermanentError{Err: err}
}

// isTransient は Textract の一時的な失敗かを判定する
//
// スロットリングと同時実行数の上限は時間を置けば解消するため Retry に委ね、文書や引数に起因する失敗は対象にしない
func isTransient(err error) bool {
	if _, ok := errors.AsType[*types.ProvisionedThroughputExceededException](err); ok {
		return true
	}
	if _, ok := errors.AsType[*types.LimitExceededException](err); ok {
		return true
	}
	if _, ok := errors.AsType[*types.ThrottlingException](err); ok {
		return true
	}
	_, ok := errors.AsType[*types.InternalServerError](err)
	return ok
}

// ParseFeatureTypes はカンマ区切りの設定値 (config.Config.TextractFeatureTypes) を FeatureTypes に変換する
func ParseFeatureTypes(spec string) ([]types.FeatureType, error) {
	var values []string
	for _, v := range strings.Split(spec, ",") {
		if v = strings.TrimSpace(v); v != "" {
			values = append(values, v)
		}
	}
	return textract.ParseFeatureTypes(values)
}
