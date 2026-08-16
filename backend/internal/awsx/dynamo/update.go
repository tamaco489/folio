package dynamo

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsdynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// UpdateStatus は既存ジョブの status を更新し、残っている errorReason を消す
//
// FAILED は理由の記録が必須のため受け付けない (MarkFailed を使う)
func (c *Client) UpdateStatus(ctx context.Context, jobID string, status Status) (Job, error) {
	if !status.Valid() {
		return Job{}, fmt.Errorf("dynamo: unknown status %q", status)
	}
	if status == StatusFailed {
		return Job{}, ErrFailedNeedsReason
	}
	return c.updateItem(ctx, jobID,
		"SET #status = :status, #updatedAt = :updatedAt REMOVE #errorReason",
		map[string]string{
			"#jobId":       attrJobID,
			"#status":      attrStatus,
			"#updatedAt":   attrUpdatedAt,
			"#errorReason": attrErrorReason,
		},
		map[string]awsdynamodbtypes.AttributeValue{
			":status":    &awsdynamodbtypes.AttributeValueMemberS{Value: string(status)},
			":updatedAt": &awsdynamodbtypes.AttributeValueMemberS{Value: formatTime(c.now())},
		},
	)
}

// MarkFailed は status を FAILED にし、失敗理由を記録する
func (c *Client) MarkFailed(ctx context.Context, jobID, reason string) (Job, error) {
	if reason == "" {
		return Job{}, ErrFailedNeedsReason
	}
	return c.updateItem(ctx, jobID,
		"SET #status = :status, #updatedAt = :updatedAt, #errorReason = :errorReason",
		map[string]string{
			"#jobId":       attrJobID,
			"#status":      attrStatus,
			"#updatedAt":   attrUpdatedAt,
			"#errorReason": attrErrorReason,
		},
		map[string]awsdynamodbtypes.AttributeValue{
			":status":      &awsdynamodbtypes.AttributeValueMemberS{Value: string(StatusFailed)},
			":updatedAt":   &awsdynamodbtypes.AttributeValueMemberS{Value: formatTime(c.now())},
			":errorReason": &awsdynamodbtypes.AttributeValueMemberS{Value: reason},
		},
	)
}

func (c *Client) updateItem(ctx context.Context, jobID, expr string, names map[string]string, values map[string]awsdynamodbtypes.AttributeValue) (Job, error) {
	out, err := c.api.UpdateItem(ctx, &awsdynamodb.UpdateItemInput{
		TableName:                 aws.String(c.tableName),
		Key:                       jobKey(jobID),
		UpdateExpression:          aws.String(expr),
		ConditionExpression:       aws.String("attribute_exists(#jobId)"),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ReturnValues:              awsdynamodbtypes.ReturnValueAllNew,
	})
	if err != nil {
		if _, ok := errors.AsType[*awsdynamodbtypes.ConditionalCheckFailedException](err); ok {
			return Job{}, fmt.Errorf("dynamo: update job %s: %w", jobID, ErrJobNotFound)
		}
		return Job{}, fmt.Errorf("dynamo: update job %s: %w", jobID, err)
	}
	return jobFromItem(out.Attributes)
}
