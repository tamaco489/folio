package textract

import "errors"

var (
	// ErrJobInProgress はジョブが未完了であることを示す
	ErrJobInProgress = errors.New("textract: job is in progress")

	// ErrJobFailed はジョブが失敗したことを示す
	ErrJobFailed = errors.New("textract: job failed")

	// ErrInvalidInput は呼び出し側の引数不備を示す
	ErrInvalidInput = errors.New("textract: invalid input")
)
