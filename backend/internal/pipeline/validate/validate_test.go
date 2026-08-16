package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

const (
	testBucket = "dev-folio-documents"
	testTable  = "dev-folio-jobs"
)

var testNow = time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)

// errS3Unused は S3 に触れてはいけない経路で触れたことを検出するための番人
var errS3Unused = errors.New("s3 must not be touched")

func newHandler(api s3.API, info PDFInfo) (*Handler, *fakeDynamo) {
	jobs := newFakeDynamo()
	h := New(
		s3.New(api, testBucket),
		dynamo.New(jobs, testTable, dynamo.WithClock(func() time.Time { return testNow })),
		info,
	)
	return h, jobs
}

// seedUpload はアップロード済みの PDF を配置し、その実体から導かれる jobId とキーを返す
func seedUpload(fake *s3test.Fake, body []byte) (string, string) {
	sum := sha256.Sum256(body)
	jobID := hex.EncodeToString(sum[:])
	key := s3.OriginalPDFKey(jobID)
	fake.Seed(testBucket, key, s3test.Object{Body: body, ContentType: "application/pdf"})
	return jobID, key
}

// assertCompact は出力が実体を含んでいないことを確かめる
//
// State 間の上限は 256KB だが、PDF の実体が紛れ込めば桁違いに超えるため、より厳しい 1KB で押さえる
func assertCompact(t *testing.T, out Output) {
	t.Helper()

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if len(b) > 1024 {
		t.Errorf("output is %d bytes, want at most 1024: %s", len(b), b)
	}
}

func TestHandleProceedsForNewUpload(t *testing.T) {
	fake := s3test.NewFake()
	jobID, key := seedUpload(fake, buildPDF(3))
	h, jobs := newHandler(fake, &fakePDF{info: pdf.Info{Pages: 3}})

	got, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: key})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got.Decision != DecisionProceed {
		t.Errorf("decision = %q, want %q", got.Decision, DecisionProceed)
	}
	if got.JobID != jobID {
		t.Errorf("jobId = %q, want %q", got.JobID, jobID)
	}
	if got.Bucket != testBucket || got.Key != key {
		t.Errorf("location = %q / %q, want %q / %q", got.Bucket, got.Key, testBucket, key)
	}
	if got.Reason != nil {
		t.Errorf("reason = %+v, want nil", got.Reason)
	}
	if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusProcessing) {
		t.Errorf("stored status = %q, want %q", status, dynamo.StatusProcessing)
	}
	assertCompact(t, got)
}

func TestHandleRejectsInvalidInput(t *testing.T) {
	tests := map[string]struct {
		body     []byte
		info     pdf.Info
		infoErr  error
		wantCode Code
	}{
		"異常系_PDF の署名がない場合_NOT_PDF で弾かれること": {
			body:     []byte(strings.Repeat("this is not a pdf. ", 100)),
			info:     pdf.Info{Pages: 1},
			wantCode: CodeNotPDF,
		},
		"異常系_ページ数が上限を超える場合_TOO_MANY_PAGES で弾かれること": {
			body:     buildPDF(1),
			info:     pdf.Info{Pages: MaxPages + 1},
			wantCode: CodeTooManyPages,
		},
		"異常系_保護された PDF の場合_ENCRYPTED で弾かれること": {
			body:     buildPDF(1),
			info:     pdf.Info{Pages: 4, Encrypted: true, Encryption: "yes (print:yes copy:no)"},
			wantCode: CodeEncrypted,
		},
		"異常系_パスワードなしで開けない場合_ENCRYPTED で弾かれること": {
			body:     buildPDF(1),
			infoErr:  pdf.ErrEncrypted,
			wantCode: CodeEncrypted,
		},
		"異常系_PDF として読めない場合_DAMAGED で弾かれること": {
			body:     buildPDF(1),
			infoErr:  pdf.ErrDamaged,
			wantCode: CodeDamaged,
		},
		"異常系_ページ数がゼロの場合_DAMAGED で弾かれること": {
			body:     buildPDF(1),
			info:     pdf.Info{Pages: 0},
			wantCode: CodeDamaged,
		},
		"境界値_ページ数が上限ちょうどの場合_弾かれないこと": {
			body: buildPDF(1),
			info: pdf.Info{Pages: MaxPages},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := s3test.NewFake()
			jobID, key := seedUpload(fake, tt.body)
			h, jobs := newHandler(fake, &fakePDF{info: tt.info, err: tt.infoErr})

			got, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: key})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			assertCompact(t, got)

			if tt.wantCode == "" {
				if got.Decision != DecisionProceed {
					t.Fatalf("decision = %q (%+v), want %q", got.Decision, got.Reason, DecisionProceed)
				}
				if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusProcessing) {
					t.Errorf("stored status = %q, want %q", status, dynamo.StatusProcessing)
				}
				return
			}

			if got.Decision != DecisionRejected {
				t.Fatalf("decision = %q, want %q", got.Decision, DecisionRejected)
			}
			if got.Reason == nil || got.Reason.Code != tt.wantCode {
				t.Fatalf("reason = %+v, want code %q", got.Reason, tt.wantCode)
			}
			if got.Reason.Message == "" {
				t.Error("reason message is empty")
			}
			if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusFailed) {
				t.Errorf("stored status = %q, want %q", status, dynamo.StatusFailed)
			}
			if reason := jobs.attr(jobID, "errorReason"); !strings.HasPrefix(reason, string(tt.wantCode)) {
				t.Errorf("errorReason = %q, want it to start with %q", reason, tt.wantCode)
			}
		})
	}
}

func TestHandleRejectsOversizedObject(t *testing.T) {
	fake := s3test.NewFake()
	jobID, key := seedUpload(fake, buildPDF(1))
	// サイズ超過は本体を取得する前に弾くため、取得されたら失敗させる
	fake.GetErr = errS3Unused
	h, jobs := newHandler(&oversizeS3{API: fake, size: MaxBytes + 1}, &fakePDF{info: pdf.Info{Pages: 1}})

	got, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: key})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got.Decision != DecisionRejected {
		t.Fatalf("decision = %q, want %q", got.Decision, DecisionRejected)
	}
	if got.Reason == nil || got.Reason.Code != CodeTooLarge {
		t.Fatalf("reason = %+v, want code %q", got.Reason, CodeTooLarge)
	}
	if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusFailed) {
		t.Errorf("stored status = %q, want %q", status, dynamo.StatusFailed)
	}
}

func TestHandleRejectsHashMismatch(t *testing.T) {
	fake := s3test.NewFake()
	// jobId をファイルの SHA-256 に揃えることが冪等性の根拠であるため、キーと実体の不一致は改竄・取り違えとして弾く
	jobID := strings.Repeat("0", 64)
	key := s3.OriginalPDFKey(jobID)
	fake.Seed(testBucket, key, s3test.Object{Body: buildPDF(1)})
	h, jobs := newHandler(fake, &fakePDF{info: pdf.Info{Pages: 1}})

	got, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: key})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got.Decision != DecisionRejected {
		t.Fatalf("decision = %q, want %q", got.Decision, DecisionRejected)
	}
	if got.Reason == nil || got.Reason.Code != CodeHashMismatch {
		t.Fatalf("reason = %+v, want code %q", got.Reason, CodeHashMismatch)
	}
	if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusFailed) {
		t.Errorf("stored status = %q, want %q", status, dynamo.StatusFailed)
	}
}

func TestHandleIdempotency(t *testing.T) {
	tests := map[string]struct {
		existing     dynamo.Status
		wantDecision Decision
		wantCode     Code
	}{
		"異常系_既存が PROCESSING の場合_二重起動を避けて SKIPPED になること": {
			existing: dynamo.StatusProcessing, wantDecision: DecisionSkipped, wantCode: CodeInProgress,
		},
		"正常系_既存が COMPLETED の場合_既存結果を返して SKIPPED になること": {
			existing: dynamo.StatusCompleted, wantDecision: DecisionSkipped, wantCode: CodeAlreadyProcessed,
		},
		"正常系_既存が REVIEW_PENDING の場合_既存結果を返して SKIPPED になること": {
			existing: dynamo.StatusReviewPending, wantDecision: DecisionSkipped, wantCode: CodeAlreadyProcessed,
		},
		"正常系_既存が FAILED の場合_再処理されて PROCEED になること": {
			existing: dynamo.StatusFailed, wantDecision: DecisionProceed,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := s3test.NewFake()
			jobID, key := seedUpload(fake, buildPDF(2))
			if tt.wantDecision == DecisionSkipped {
				// 冪等性の判定は S3 に触れる前に済ませるため、触れたら失敗させる
				fake.GetErr = errS3Unused
				fake.HeadErr = errS3Unused
			}
			h, jobs := newHandler(fake, &fakePDF{info: pdf.Info{Pages: 2}})
			jobs.seed(jobID, tt.existing)

			got, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: key})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			assertCompact(t, got)

			if got.Decision != tt.wantDecision {
				t.Fatalf("decision = %q, want %q", got.Decision, tt.wantDecision)
			}
			if got.JobID != jobID || got.Key != key {
				t.Errorf("output = %q / %q, want %q / %q", got.JobID, got.Key, jobID, key)
			}

			if tt.wantDecision == DecisionProceed {
				if got.Reason != nil {
					t.Errorf("reason = %+v, want nil", got.Reason)
				}
				if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusProcessing) {
					t.Errorf("stored status = %q, want %q", status, dynamo.StatusProcessing)
				}
				return
			}

			if got.Reason == nil || got.Reason.Code != tt.wantCode {
				t.Fatalf("reason = %+v, want code %q", got.Reason, tt.wantCode)
			}
			if status := jobs.attr(jobID, "status"); status != string(tt.existing) {
				t.Errorf("stored status = %q, want the existing %q", status, tt.existing)
			}
			if updatedAt := jobs.attr(jobID, "updatedAt"); updatedAt != seededAt {
				t.Errorf("updatedAt = %q, want the existing record to be untouched", updatedAt)
			}
		})
	}
}

func TestHandleFailsOnMisroutedEvent(t *testing.T) {
	fake := s3test.NewFake()
	_, key := seedUpload(fake, buildPDF(1))

	tests := map[string]Input{
		"異常系_設定と異なるバケットの場合_エラーになること":      {Bucket: "other-bucket", Key: key},
		"異常系_uploads 配下でないキーの場合_エラーになること": {Bucket: testBucket, Key: s3.PageImageKey("job-1", 1)},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h, jobs := newHandler(fake, &fakePDF{info: pdf.Info{Pages: 1}})

			if _, err := h.Handle(context.Background(), in); err == nil {
				t.Fatal("Handle() error = nil, want error")
			}
			if len(jobs.items) != 0 {
				t.Errorf("job was registered for a misrouted event: %v", jobs.items)
			}
		})
	}
}

func TestHandleFailsWhenObjectIsMissing(t *testing.T) {
	fake := s3test.NewFake()
	jobID := strings.Repeat("a", 64)
	h, jobs := newHandler(fake, &fakePDF{info: pdf.Info{Pages: 1}})

	// 取得できないのは入力の不備ではなく一時障害でもありうるため、判定ではなくエラーとして Step Functions に委ねる
	_, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: s3.OriginalPDFKey(jobID)})
	if !errors.Is(err, s3.ErrNotFound) {
		t.Fatalf("Handle() error = %v, want %v", err, s3.ErrNotFound)
	}
	if !jobs.exists(jobID) {
		t.Error("job should stay registered so that the retry is gated by the same record")
	}
}

func TestCheckSize(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		size     int64
		wantCode Code
	}{
		"正常系_十分に小さい場合_通ること":         {size: 1024},
		"境界値_上限ちょうどの場合_通ること":        {size: MaxBytes},
		"境界値_上限を 1 バイト超える場合_弾かれること": {size: MaxBytes + 1, wantCode: CodeTooLarge},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := checkSize(tt.size)
			if tt.wantCode == "" {
				if got != nil {
					t.Fatalf("checkSize(%d) = %+v, want nil", tt.size, got)
				}
				return
			}
			if got == nil || got.Code != tt.wantCode {
				t.Fatalf("checkSize(%d) = %+v, want code %q", tt.size, got, tt.wantCode)
			}
		})
	}
}

func TestCheckPages(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pages    int
		wantCode Code
	}{
		"正常系_評価対象と同程度の場合_通ること":      {pages: 20},
		"境界値_上限ちょうどの場合_通ること":        {pages: MaxPages},
		"境界値_上限を 1 ページ超える場合_弾かれること": {pages: MaxPages + 1, wantCode: CodeTooManyPages},
		"境界値_1 ページの場合_通ること":         {pages: 1},
		"異常系_ページ数がゼロの場合_弾かれること":     {pages: 0, wantCode: CodeDamaged},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := checkPages(tt.pages)
			if tt.wantCode == "" {
				if got != nil {
					t.Fatalf("checkPages(%d) = %+v, want nil", tt.pages, got)
				}
				return
			}
			if got == nil || got.Code != tt.wantCode {
				t.Fatalf("checkPages(%d) = %+v, want code %q", tt.pages, got, tt.wantCode)
			}
		})
	}
}

func TestLooksLikePDF(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		head []byte
		want bool
	}{
		"正常系_先頭に署名がある場合_PDF と判定されること":       {head: []byte("%PDF-1.7\n1 0 obj"), want: true},
		"正常系_署名の前に余分なバイトがある場合_PDF と判定されること": {head: append([]byte("junk\n"), "%PDF-1.4"...), want: true},
		"異常系_署名がない場合_PDF と判定されないこと":         {head: []byte("PK\x03\x04zip archive"), want: false},
		"境界値_空の場合_PDF と判定されないこと":            {head: nil, want: false},
		"境界値_署名の途中で終わる場合_PDF と判定されないこと":     {head: []byte("%PDF"), want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := looksLikePDF(tt.head); got != tt.want {
				t.Errorf("looksLikePDF(%q) = %v, want %v", tt.head, got, tt.want)
			}
		})
	}
}

// TestHandleWithPoppler は pdf.Runner を差し替えずに経路全体を通す
//
// PDFInfo のフェイクでは pdfinfo の呼び出しと出力の解釈が検証から抜けるため、poppler がある環境でだけ実物を通す
func TestHandleWithPoppler(t *testing.T) {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skipf("pdfinfo が見つからないためスキップする: %v", err)
	}
	runner := pdf.NewRunner(pdf.WithBinDir(""), pdf.WithTimeout(time.Minute))

	tests := map[string]struct {
		body     []byte
		wantCode Code
	}{
		"正常系_2 ページの PDF の場合_通ること": {body: buildPDF(2)},
		"異常系_署名だけで中身が壊れている場合_DAMAGED で弾かれること": {
			body:     []byte("%PDF-1.4\nthis is not a valid pdf body\n"),
			wantCode: CodeDamaged,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := s3test.NewFake()
			jobID, key := seedUpload(fake, tt.body)
			h, jobs := newHandler(fake, runner)

			got, err := h.Handle(context.Background(), Input{Bucket: testBucket, Key: key})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if tt.wantCode == "" {
				if got.Decision != DecisionProceed {
					t.Fatalf("decision = %q (%+v), want %q", got.Decision, got.Reason, DecisionProceed)
				}
				if status := jobs.attr(jobID, "status"); status != string(dynamo.StatusProcessing) {
					t.Errorf("stored status = %q, want %q", status, dynamo.StatusProcessing)
				}
				return
			}
			if got.Decision != DecisionRejected {
				t.Fatalf("decision = %q, want %q", got.Decision, DecisionRejected)
			}
			if got.Reason == nil || got.Reason.Code != tt.wantCode {
				t.Fatalf("reason = %+v, want code %q", got.Reason, tt.wantCode)
			}
		})
	}
}
