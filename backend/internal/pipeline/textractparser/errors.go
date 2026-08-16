package textractparser

import (
	"errors"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
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
	if _, ok := errors.AsType[*awstextracttypes.ProvisionedThroughputExceededException](err); ok {
		return true
	}
	if _, ok := errors.AsType[*awstextracttypes.LimitExceededException](err); ok {
		return true
	}
	if _, ok := errors.AsType[*awstextracttypes.ThrottlingException](err); ok {
		return true
	}
	_, ok := errors.AsType[*awstextracttypes.InternalServerError](err)
	return ok
}
