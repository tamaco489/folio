package dynamo

import (
	"errors"
	"fmt"
)

var (
	// ErrJobNotFound は対象の jobId がテーブルに存在しないことを表す
	ErrJobNotFound = errors.New("dynamo: job not found")

	// ErrFailedNeedsReason は FAILED への遷移に errorReason が必須であることを表す
	ErrFailedNeedsReason = errors.New("dynamo: FAILED requires an error reason, use MarkFailed")
)

// JobExistsError は条件付き書き込みが既存レコードに弾かれたことを表す
//
// Existing には条件失敗時に DynamoDB が返す旧レコードが入る
// 呼び出し側はこの status を見て、既存結果を返すか処理を打ち切るかを判断する
type JobExistsError struct {
	JobID       string
	Existing    Job
	ExistingErr error // ExistingErr は旧レコードの復元に失敗した場合の理由
}

func (e *JobExistsError) Error() string {
	if e.ExistingErr != nil {
		return fmt.Sprintf("dynamo: job %s already exists (existing record unavailable: %v)", e.JobID, e.ExistingErr)
	}
	return fmt.Sprintf("dynamo: job %s already exists with status %s", e.JobID, e.Existing.Status)
}

// AsJobExists は err が JobExistsError かどうかを判定する
func AsJobExists(err error) (*JobExistsError, bool) {
	if target, ok := errors.AsType[*JobExistsError](err); ok {
		return target, true
	}
	return nil, false
}
