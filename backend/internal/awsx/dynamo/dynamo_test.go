package dynamo

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const testTable = "dev-folio-jobs"

var baseTime = time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)

func newTestClient(f *fakeDynamo, now time.Time) *Client {
	return New(f, testTable, WithClock(func() time.Time { return now }))
}

func TestRegisterJobNew(t *testing.T) {
	f := newFakeDynamo()
	c := newTestClient(f, baseTime)

	job, err := c.RegisterJob(context.Background(), "hash-1", "paper.pdf")
	if err != nil {
		t.Fatalf("RegisterJob() error = %v", err)
	}
	if job.Status != StatusProcessing {
		t.Errorf("status = %v, want %v", job.Status, StatusProcessing)
	}
	if !job.CreatedAt.Equal(baseTime) || !job.UpdatedAt.Equal(baseTime) {
		t.Errorf("timestamps = %v / %v, want %v", job.CreatedAt, job.UpdatedAt, baseTime)
	}

	stored := f.get("hash-1")
	if stored == nil {
		t.Fatal("item was not stored")
	}
	got := make([]string, 0, len(stored))
	for name := range stored {
		got = append(got, name)
	}
	sort.Strings(got)
	// errorReason は値がないときに書かない (それ以外の属性は Notion 定義の 6 つに限定する)
	want := []string{"createdAt", "filename", "jobId", "status", "updatedAt"}
	if len(got) != len(want) {
		t.Fatalf("attributes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attributes = %v, want %v", got, want)
		}
	}
	if aws.ToString(f.lastPut.ConditionExpression) != "attribute_not_exists(#jobId) OR #status = :failed" {
		t.Errorf("unexpected condition expression: %q", aws.ToString(f.lastPut.ConditionExpression))
	}
	if aws.ToString(f.lastPut.TableName) != testTable {
		t.Errorf("table = %q, want %q", aws.ToString(f.lastPut.TableName), testTable)
	}
}

func TestRegisterJobConflict(t *testing.T) {
	tests := []struct {
		name   string
		status Status
	}{
		{"処理中は二重起動を防ぐ", StatusProcessing},
		{"完了済みは再処理しない", StatusCompleted},
		{"レビュー待ちは再処理しない", StatusReviewPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeDynamo()
			f.seed(Job{
				JobID:     "hash-1",
				Status:    tt.status,
				Filename:  "paper.pdf",
				CreatedAt: baseTime.Add(-time.Hour),
				UpdatedAt: baseTime.Add(-time.Hour),
			})
			c := newTestClient(f, baseTime)

			_, err := c.RegisterJob(context.Background(), "hash-1", "paper.pdf")
			exists, ok := AsJobExists(err)
			if !ok {
				t.Fatalf("error = %v, want JobExistsError", err)
			}
			if exists.ExistingErr != nil {
				t.Fatalf("existing record unavailable: %v", exists.ExistingErr)
			}
			if exists.Existing.Status != tt.status {
				t.Errorf("existing status = %v, want %v", exists.Existing.Status, tt.status)
			}
			if exists.Existing.Status.Reprocessable() {
				t.Errorf("status %v must not be reprocessable", tt.status)
			}
			if got := attrString(f.get("hash-1"), attrStatus); got != string(tt.status) {
				t.Errorf("stored status = %q, want %q", got, tt.status)
			}
			if got := attrString(f.get("hash-1"), attrCreatedAt); got != formatTime(baseTime.Add(-time.Hour)) {
				t.Errorf("createdAt was overwritten: %q", got)
			}
		})
	}
}

func TestRegisterJobAfterFailed(t *testing.T) {
	f := newFakeDynamo()
	f.seed(Job{
		JobID:       "hash-1",
		Status:      StatusFailed,
		Filename:    "paper.pdf",
		CreatedAt:   baseTime.Add(-time.Hour),
		UpdatedAt:   baseTime.Add(-time.Hour),
		ErrorReason: "textract timeout",
	})
	c := newTestClient(f, baseTime)

	job, err := c.RegisterJob(context.Background(), "hash-1", "paper.pdf")
	if err != nil {
		t.Fatalf("RegisterJob() error = %v, want success for FAILED", err)
	}
	if job.Status != StatusProcessing {
		t.Errorf("status = %v, want %v", job.Status, StatusProcessing)
	}
	stored := f.get("hash-1")
	if _, ok := stored[attrErrorReason]; ok {
		t.Error("errorReason should be cleared on reprocess")
	}
	if got := attrString(stored, attrCreatedAt); got != formatTime(baseTime) {
		t.Errorf("createdAt = %q, want the new submission time %q", got, formatTime(baseTime))
	}
}

func TestRegisterJobIsIdempotentForRepeatedSubmission(t *testing.T) {
	f := newFakeDynamo()
	c := newTestClient(f, baseTime)

	if _, err := c.RegisterJob(context.Background(), "hash-1", "paper.pdf"); err != nil {
		t.Fatalf("first RegisterJob() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.RegisterJob(context.Background(), "hash-1", "paper.pdf"); err == nil {
			t.Fatalf("resubmission %d succeeded, want conflict", i)
		}
	}
	if got := attrString(f.get("hash-1"), attrStatus); got != string(StatusProcessing) {
		t.Errorf("status = %q, want %q", got, StatusProcessing)
	}
}

func TestGetJob(t *testing.T) {
	f := newFakeDynamo()
	f.seed(Job{
		JobID:       "hash-1",
		Status:      StatusFailed,
		Filename:    "paper.pdf",
		CreatedAt:   baseTime,
		UpdatedAt:   baseTime.Add(time.Minute),
		ErrorReason: "bedrock throttled",
	})
	c := newTestClient(f, baseTime)

	job, err := c.GetJob(context.Background(), "hash-1")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Filename != "paper.pdf" || job.ErrorReason != "bedrock throttled" {
		t.Errorf("job = %+v", job)
	}
	if !job.CreatedAt.Equal(baseTime) || !job.UpdatedAt.Equal(baseTime.Add(time.Minute)) {
		t.Errorf("timestamps = %v / %v", job.CreatedAt, job.UpdatedAt)
	}
	if !aws.ToBool(f.lastGet.ConsistentRead) {
		t.Error("GetItem should use a consistent read")
	}
}

func TestGetJobNotFound(t *testing.T) {
	c := newTestClient(newFakeDynamo(), baseTime)

	if _, err := c.GetJob(context.Background(), "missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("error = %v, want ErrJobNotFound", err)
	}
}

func TestUpdateStatus(t *testing.T) {
	f := newFakeDynamo()
	f.seed(Job{
		JobID:       "hash-1",
		Status:      StatusProcessing,
		Filename:    "paper.pdf",
		CreatedAt:   baseTime,
		UpdatedAt:   baseTime,
		ErrorReason: "previous failure",
	})
	updatedAt := baseTime.Add(2 * time.Minute)
	c := newTestClient(f, updatedAt)

	job, err := c.UpdateStatus(context.Background(), "hash-1", StatusReviewPending)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if job.Status != StatusReviewPending {
		t.Errorf("status = %v, want %v", job.Status, StatusReviewPending)
	}
	if !job.UpdatedAt.Equal(updatedAt) {
		t.Errorf("updatedAt = %v, want %v", job.UpdatedAt, updatedAt)
	}
	if !job.CreatedAt.Equal(baseTime) {
		t.Errorf("createdAt = %v, want %v", job.CreatedAt, baseTime)
	}
	if job.ErrorReason != "" {
		t.Errorf("errorReason = %q, want empty", job.ErrorReason)
	}
	if _, ok := f.get("hash-1")[attrErrorReason]; ok {
		t.Error("errorReason should be removed from the stored item")
	}
}

func TestUpdateStatusRejectsFailed(t *testing.T) {
	f := newFakeDynamo()
	f.seed(Job{JobID: "hash-1", Status: StatusProcessing, Filename: "paper.pdf", CreatedAt: baseTime, UpdatedAt: baseTime})
	c := newTestClient(f, baseTime)

	if _, err := c.UpdateStatus(context.Background(), "hash-1", StatusFailed); !errors.Is(err, ErrFailedNeedsReason) {
		t.Fatalf("error = %v, want ErrFailedNeedsReason", err)
	}
	if _, err := c.UpdateStatus(context.Background(), "hash-1", Status("UNKNOWN")); err == nil {
		t.Fatal("unknown status should be rejected")
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	c := newTestClient(newFakeDynamo(), baseTime)

	if _, err := c.UpdateStatus(context.Background(), "missing", StatusCompleted); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("error = %v, want ErrJobNotFound", err)
	}
}

func TestMarkFailed(t *testing.T) {
	f := newFakeDynamo()
	f.seed(Job{JobID: "hash-1", Status: StatusProcessing, Filename: "paper.pdf", CreatedAt: baseTime, UpdatedAt: baseTime})
	failedAt := baseTime.Add(5 * time.Minute)
	c := newTestClient(f, failedAt)

	job, err := c.MarkFailed(context.Background(), "hash-1", "textract timeout")
	if err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	if job.Status != StatusFailed || job.ErrorReason != "textract timeout" {
		t.Errorf("job = %+v", job)
	}
	if !job.UpdatedAt.Equal(failedAt) {
		t.Errorf("updatedAt = %v, want %v", job.UpdatedAt, failedAt)
	}

	// FAILED は再処理を許可するため、同じ jobId の再投入が通る
	if _, err := c.RegisterJob(context.Background(), "hash-1", "paper.pdf"); err != nil {
		t.Fatalf("RegisterJob() after MarkFailed error = %v", err)
	}
}

func TestMarkFailedRequiresReason(t *testing.T) {
	c := newTestClient(newFakeDynamo(), baseTime)

	if _, err := c.MarkFailed(context.Background(), "hash-1", ""); !errors.Is(err, ErrFailedNeedsReason) {
		t.Fatalf("error = %v, want ErrFailedNeedsReason", err)
	}
}

func TestListByStatusNewestFirst(t *testing.T) {
	f := newFakeDynamo()
	f.seed(Job{JobID: "old", Status: StatusReviewPending, Filename: "a.pdf", CreatedAt: baseTime, UpdatedAt: baseTime})
	f.seed(Job{JobID: "new", Status: StatusReviewPending, Filename: "b.pdf", CreatedAt: baseTime, UpdatedAt: baseTime.Add(time.Hour)})
	f.seed(Job{JobID: "other", Status: StatusCompleted, Filename: "c.pdf", CreatedAt: baseTime, UpdatedAt: baseTime.Add(2 * time.Hour)})
	c := newTestClient(f, baseTime)

	jobs, err := c.ListByStatus(context.Background(), StatusReviewPending, 0)
	if err != nil {
		t.Fatalf("ListByStatus() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(jobs))
	}
	if jobs[0].JobID != "new" || jobs[1].JobID != "old" {
		t.Errorf("order = %s, %s, want new, old", jobs[0].JobID, jobs[1].JobID)
	}
	if aws.ToString(f.lastQuery.IndexName) != IndexStatusUpdatedAt {
		t.Errorf("index = %q, want %q", aws.ToString(f.lastQuery.IndexName), IndexStatusUpdatedAt)
	}
	if aws.ToBool(f.lastQuery.ScanIndexForward) {
		t.Error("ScanIndexForward should be false to read newest first")
	}
	if f.lastQuery.Limit != nil {
		t.Errorf("Limit = %v, want unset", *f.lastQuery.Limit)
	}
}

func TestListByStatusLimit(t *testing.T) {
	f := newFakeDynamo()
	for _, id := range []string{"a", "b", "c"} {
		f.seed(Job{JobID: id, Status: StatusReviewPending, Filename: id + ".pdf", CreatedAt: baseTime, UpdatedAt: baseTime})
	}
	c := newTestClient(f, baseTime)

	jobs, err := c.ListByStatus(context.Background(), StatusReviewPending, 2)
	if err != nil {
		t.Fatalf("ListByStatus() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(jobs))
	}
	if _, err := c.ListByStatus(context.Background(), Status("UNKNOWN"), 0); err == nil {
		t.Fatal("unknown status should be rejected")
	}
}

func TestStatusReprocessable(t *testing.T) {
	tests := map[Status]bool{
		StatusProcessing:    false,
		StatusCompleted:     false,
		StatusReviewPending: false,
		StatusFailed:        true,
	}
	for status, want := range tests {
		if got := status.Reprocessable(); got != want {
			t.Errorf("%v.Reprocessable() = %v, want %v", status, got, want)
		}
		if !status.Valid() {
			t.Errorf("%v should be valid", status)
		}
	}
	if Status("UNKNOWN").Valid() {
		t.Error("UNKNOWN should be invalid")
	}
}

func TestTimeFormatSortsLexicographically(t *testing.T) {
	// GSI のソートキーは文字列比較になるため、桁数が変わると順序が壊れる
	// 500ms と 550ms は末尾ゼロを省く可変桁フォーマットだと ".5Z" > ".55Z" となり逆転する
	earlier := formatTime(baseTime.Add(500 * time.Millisecond))
	later := formatTime(baseTime.Add(550 * time.Millisecond))
	if earlier >= later {
		t.Errorf("%q should sort before %q", earlier, later)
	}
	if len(earlier) != len(later) {
		t.Errorf("timestamp width is not fixed: %q vs %q", earlier, later)
	}
}
