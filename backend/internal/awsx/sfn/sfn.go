// Package sfn は Step Functions の操作を awsx 層に閉じ込める
//
// コールバックパターン (waitForTaskToken) で待つ Task へ、タスクトークンを使って完了と失敗を応答するためにある
package sfn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

// API は本パッケージが使う Step Functions の操作だけを切り出したインタフェース
type API interface {
	SendTaskSuccess(ctx context.Context, params *awssfn.SendTaskSuccessInput, optFns ...func(*awssfn.Options)) (*awssfn.SendTaskSuccessOutput, error)
	SendTaskFailure(ctx context.Context, params *awssfn.SendTaskFailureInput, optFns ...func(*awssfn.Options)) (*awssfn.SendTaskFailureOutput, error)
}

var _ API = (*awssfn.Client)(nil)

var (
	// ErrTaskGone はタスクトークンで応答できる相手がもう存在しないことを表す
	//
	// SDK は InvalidToken / TaskTimedOut / TaskDoesNotExist の 3 型で返すが、いずれも再試行しても応答は届かないため 1 つに畳む
	ErrTaskGone = errors.New("sfn: task is no longer waiting for a response")

	// ErrInvalidInput は呼び出し側の引数不備を表す
	ErrInvalidInput = errors.New("sfn: invalid input")
)

// Client は awsx 層が公開する Step Functions クライアント
type Client struct {
	api API
}

// New は SDK のクライアントをラップする
func New(api API) *Client {
	return &Client{api: api}
}

// SendTaskSuccess は output を JSON にして Task の出力として返し、待機中の State Machine を再開させる
func (c *Client) SendTaskSuccess(ctx context.Context, taskToken string, output any) error {
	if taskToken == "" {
		return fmt.Errorf("%w: task token is required", ErrInvalidInput)
	}
	b, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("sfn: encode task output: %w", err)
	}
	if _, err := c.api.SendTaskSuccess(ctx, &awssfn.SendTaskSuccessInput{
		TaskToken: aws.String(taskToken),
		Output:    aws.String(string(b)),
	}); err != nil {
		return wrapErr("send task success", err)
	}
	return nil
}

// SendTaskFailure は Task を失敗として終わらせる
//
// errorCode は Catch の ErrorEquals で照合される短い種別、cause は人と後段が読むための説明 (JSON 文字列を想定)
func (c *Client) SendTaskFailure(ctx context.Context, taskToken, errorCode, cause string) error {
	if taskToken == "" {
		return fmt.Errorf("%w: task token is required", ErrInvalidInput)
	}
	if errorCode == "" {
		return fmt.Errorf("%w: error code is required", ErrInvalidInput)
	}
	if _, err := c.api.SendTaskFailure(ctx, &awssfn.SendTaskFailureInput{
		TaskToken: aws.String(taskToken),
		Error:     aws.String(errorCode),
		Cause:     aws.String(cause),
	}); err != nil {
		return wrapErr("send task failure", err)
	}
	return nil
}

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
	if _, ok := errors.AsType[*types.InvalidToken](err); ok {
		return true
	}
	if _, ok := errors.AsType[*types.TaskTimedOut](err); ok {
		return true
	}
	_, ok := errors.AsType[*types.TaskDoesNotExist](err)
	return ok
}
