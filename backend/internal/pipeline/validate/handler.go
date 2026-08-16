package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

// Input は検証対象の PDF が置かれた位置
//
// S3 イベント通知の全体ではなく 2 値だけを受け取るのは、Step Functions の入力整形 (#30) が未確定であり、通知形式に実装を縛られないようにするため
type Input struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
}

// Output は後続の State に渡す判定結果
type Output struct {
	JobID    string   `json:"jobId"`
	Bucket   string   `json:"bucket"`
	Key      string   `json:"key"`
	Decision Decision `json:"decision"`
	Reason   *Reason  `json:"reason,omitempty"`
}

// PDFInfo は pdf.Runner のうち本パッケージが利用する範囲
//
// poppler を持たない環境でもページ数と保護状態の分岐を検証できるようインタフェースで受け取る
type PDFInfo interface {
	Info(ctx context.Context, pdfPath string) (pdf.Info, error)
}

var _ PDFInfo = (*pdf.Runner)(nil)

// Handler は入力の妥当性判定と冪等性チェックをまとめて担う
type Handler struct {
	docs *s3.Client
	jobs *dynamo.Client
	pdf  PDFInfo
}

// New はハンドラを組み立てる
func New(docs *s3.Client, jobs *dynamo.Client, info PDFInfo) *Handler {
	return &Handler{docs: docs, jobs: jobs, pdf: info}
}

// Handle は 1 件のアップロードを検証し、後続に進めるかどうかを決める
//
// 冪等性の判定を検証より先に行う
//   - S3 のイベント通知は同じオブジェクトに対して複数回届きうるため、重複起動を PDF の取得と pdfinfo の実行に到達させない
//   - MarkFailed は既存レコードを前提とするため、検証の失敗を errorReason に残すには先に登録されている必要がある
func (h *Handler) Handle(ctx context.Context, in Input) (Output, error) {
	if in.Bucket != h.docs.Bucket() {
		return Output{}, fmt.Errorf("validate: bucket %q is not the documents bucket %q", in.Bucket, h.docs.Bucket())
	}
	jobID, err := s3.JobIDFromUploadKey(in.Key)
	if err != nil {
		return Output{}, fmt.Errorf("validate: %w", err)
	}

	if _, err := h.jobs.RegisterJob(ctx, jobID, in.Key); err != nil {
		return h.decideOnConflict(in, jobID, err)
	}

	reason, err := h.validate(ctx, in.Key, jobID)
	if err != nil {
		return Output{}, err
	}
	if reason != nil {
		if _, err := h.jobs.MarkFailed(ctx, jobID, reason.errorReason()); err != nil {
			return Output{}, fmt.Errorf("validate: record failure of job %s: %w", jobID, err)
		}
		return newOutput(in, jobID, DecisionRejected, reason), nil
	}
	return newOutput(in, jobID, DecisionProceed, nil), nil
}

// decideOnConflict は条件付き書き込みが弾かれたときの扱いを決める
//
// 旧レコードは JobExistsError に載って返るため、status を読むための追加の取得は行わない
func (h *Handler) decideOnConflict(in Input, jobID string, err error) (Output, error) {
	exists, ok := dynamo.AsJobExists(err)
	if !ok {
		return Output{}, fmt.Errorf("validate: register job %s: %w", jobID, err)
	}
	if exists.ExistingErr != nil {
		return Output{}, fmt.Errorf("validate: register job %s: %w", jobID, exists.ExistingErr)
	}

	status := exists.Existing.Status
	if status.Reprocessable() {
		// FAILED は条件式が書き込みを通すため、ここに来るのは書き込みと競合した別の実行がレコードを差し替えた場合に限られる
		return Output{}, fmt.Errorf("validate: register job %s: conflicted with a reprocessable status %s", jobID, status)
	}

	switch status {
	case dynamo.StatusProcessing:
		return newOutput(in, jobID, DecisionSkipped, &Reason{
			Code:    CodeInProgress,
			Message: fmt.Sprintf("job %s is already running", jobID),
		}), nil

	case dynamo.StatusCompleted, dynamo.StatusReviewPending:
		return newOutput(in, jobID, DecisionSkipped, &Reason{
			Code:    CodeAlreadyProcessed,
			Message: fmt.Sprintf("job %s already finished with status %s", jobID, status),
		}), nil

	default:
		return Output{}, fmt.Errorf("validate: register job %s: unknown status %s", jobID, status)
	}
}

// validate は入力を検証し、弾く場合だけ理由を返す
//
// 判定はコストの低い順に並べる (サイズ → マジックバイト → pdfinfo)
func (h *Handler) validate(ctx context.Context, key, jobID string) (*Reason, error) {
	info, err := h.docs.Head(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("validate: head %q: %w", key, err)
	}
	if reason := checkSize(info.Size); reason != nil {
		return reason, nil
	}

	path, sum, err := h.download(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(path) }()

	head, err := readHead(path)
	if err != nil {
		return nil, err
	}
	if !looksLikePDF(head) {
		return &Reason{
			Code:    CodeNotPDF,
			Message: fmt.Sprintf("no %%PDF- header found in the first %d bytes", headerWindow),
		}, nil
	}
	if sum != jobID {
		return &Reason{
			Code:    CodeHashMismatch,
			Message: fmt.Sprintf("sha-256 %s does not match the job id in the key", sum),
		}, nil
	}

	docInfo, err := h.pdf.Info(ctx, path)
	if err != nil {
		switch {
		case errors.Is(err, pdf.ErrEncrypted):
			return &Reason{Code: CodeEncrypted, Message: "document could not be opened without a password"}, nil
		case errors.Is(err, pdf.ErrDamaged):
			return &Reason{Code: CodeDamaged, Message: "document could not be read as a pdf"}, nil
		}
		return nil, fmt.Errorf("validate: inspect %q: %w", key, err)
	}
	if docInfo.Encrypted {
		return &Reason{Code: CodeEncrypted, Message: fmt.Sprintf("document is protected (%s)", docInfo.Encryption)}, nil
	}
	return checkPages(docInfo.Pages), nil
}

// download はオブジェクトを一時ファイルへ落としつつ SHA-256 を計算する
//
// 500MB までを想定するためメモリには載せず、ハッシュは同じストリームから同時に取る
func (h *Handler) download(ctx context.Context, key string) (string, string, error) {
	f, err := os.CreateTemp("", "folio-validate-*.pdf")
	if err != nil {
		return "", "", fmt.Errorf("validate: create temp file: %w", err)
	}
	defer f.Close()

	sum := sha256.New()
	if _, err := h.docs.Download(ctx, key, io.MultiWriter(f, sum)); err != nil {
		_ = os.Remove(f.Name())
		return "", "", fmt.Errorf("validate: download %q: %w", key, err)
	}
	return f.Name(), hex.EncodeToString(sum.Sum(nil)), nil
}

func newOutput(in Input, jobID string, decision Decision, reason *Reason) Output {
	return Output{
		JobID:    jobID,
		Bucket:   in.Bucket,
		Key:      in.Key,
		Decision: decision,
		Reason:   reason,
	}
}
