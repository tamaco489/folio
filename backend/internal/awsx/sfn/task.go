package sfn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
)

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
