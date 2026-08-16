package sfn

import (
	"context"

	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
)

// API は本パッケージが使う Step Functions の操作だけを切り出したインタフェース
type API interface {
	SendTaskSuccess(ctx context.Context, params *awssfn.SendTaskSuccessInput, optFns ...func(*awssfn.Options)) (*awssfn.SendTaskSuccessOutput, error)
	SendTaskFailure(ctx context.Context, params *awssfn.SendTaskFailureInput, optFns ...func(*awssfn.Options)) (*awssfn.SendTaskFailureOutput, error)
}

var _ API = (*awssfn.Client)(nil)
