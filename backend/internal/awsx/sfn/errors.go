package sfn

import (
	"errors"
	"fmt"

	awssfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

var (
	// ErrTaskGone はタスクトークンで応答できる相手がもう存在しないことを表す
	//
	// SDK は InvalidToken / TaskTimedOut / TaskDoesNotExist の 3 型で返すが、いずれも再試行しても応答は届かないため 1 つに畳む
	ErrTaskGone = errors.New("sfn: task is no longer waiting for a response")

	// ErrInvalidInput は呼び出し側の引数不備を表す
	ErrInvalidInput = errors.New("sfn: invalid input")
)

func wrapErr(op string, err error) error {
	if isTaskGone(err) {
		return fmt.Errorf("sfn: %s: %w: %w", op, ErrTaskGone, err)
	}
	return fmt.Errorf("sfn: %s: %w", op, err)
}

// isTaskGone は SDK が返す「もう応答できない」3 系統を吸収する
//   - InvalidToken はトークンが壊れているか別の State Machine のもの
//   - TaskTimedOut は Task の TimeoutSeconds / HeartbeatSeconds を過ぎたか既に閉じている
//   - TaskDoesNotExist は Task が存在しない
func isTaskGone(err error) bool {
	if _, ok := errors.AsType[*awssfntypes.InvalidToken](err); ok {
		return true
	}
	if _, ok := errors.AsType[*awssfntypes.TaskTimedOut](err); ok {
		return true
	}
	_, ok := errors.AsType[*awssfntypes.TaskDoesNotExist](err)
	return ok
}
