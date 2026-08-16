package finalize

import (
	"errors"
	"fmt"
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
