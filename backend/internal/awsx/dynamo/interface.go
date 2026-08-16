package dynamo

import (
	"context"

	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// API は Client が利用する DynamoDB の操作 (テストではフェイクに差し替える)
type API interface {
	PutItem(ctx context.Context, params *awsdynamodb.PutItemInput, optFns ...func(*awsdynamodb.Options)) (*awsdynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, params *awsdynamodb.GetItemInput, optFns ...func(*awsdynamodb.Options)) (*awsdynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, params *awsdynamodb.UpdateItemInput, optFns ...func(*awsdynamodb.Options)) (*awsdynamodb.UpdateItemOutput, error)
	Query(ctx context.Context, params *awsdynamodb.QueryInput, optFns ...func(*awsdynamodb.Options)) (*awsdynamodb.QueryOutput, error)
}

var _ API = (*awsdynamodb.Client)(nil)
