package dynamo

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsdynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ListByStatus は GSI で同じ status のジョブを updatedAt の新しい順に取得する
//
// limit が 0 以下の場合は DynamoDB の 1 ページ分をそのまま返す
func (c *Client) ListByStatus(ctx context.Context, status Status, limit int32) ([]Job, error) {
	if !status.Valid() {
		return nil, fmt.Errorf("dynamo: unknown status %q", status)
	}
	in := &awsdynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		IndexName:              aws.String(IndexStatusUpdatedAt),
		KeyConditionExpression: aws.String("#status = :status"),
		ExpressionAttributeNames: map[string]string{
			"#status": attrStatus,
		},
		ExpressionAttributeValues: map[string]awsdynamodbtypes.AttributeValue{
			":status": &awsdynamodbtypes.AttributeValueMemberS{Value: string(status)},
		},
		ScanIndexForward: aws.Bool(false),
	}
	if limit > 0 {
		in.Limit = aws.Int32(limit)
	}
	out, err := c.api.Query(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("dynamo: query status %s: %w", status, err)
	}
	jobs := make([]Job, 0, len(out.Items))
	for _, item := range out.Items {
		job, err := jobFromItem(item)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
