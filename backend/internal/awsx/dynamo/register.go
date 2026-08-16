package dynamo

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsdynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// RegisterJob は条件付き書き込みでジョブを登録する
//
// jobId はファイルの SHA-256 なので、同じ PDF の再投入は同じ jobId になる
//   - 未登録か FAILED のときだけ書き込みを通すことで、専用テーブルを持たずに「成功したものだけ弾く」冪等性を 1 回の書き込みで成立させる
//   - 弾かれた場合は JobExistsError に旧レコードを載せて返すので、呼び出し側は追加の読み取りなしに PROCESSING か COMPLETED かを判定できる
func (c *Client) RegisterJob(ctx context.Context, jobID, filename string) (Job, error) {
	now := c.now().UTC()
	job := Job{
		JobID:     jobID,
		Status:    StatusProcessing,
		Filename:  filename,
		CreatedAt: now,
		UpdatedAt: now,
	}
	item, err := job.item()
	if err != nil {
		return Job{}, err
	}

	_, err = c.api.PutItem(ctx, &awsdynamodb.PutItemInput{
		TableName:           aws.String(c.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(#jobId) OR #status = :failed"),
		ExpressionAttributeNames: map[string]string{
			"#jobId":  attrJobID,
			"#status": attrStatus,
		},
		ExpressionAttributeValues: map[string]awsdynamodbtypes.AttributeValue{
			":failed": &awsdynamodbtypes.AttributeValueMemberS{Value: string(StatusFailed)},
		},
		ReturnValuesOnConditionCheckFailure: awsdynamodbtypes.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err != nil {
		if condErr, ok := errors.AsType[*awsdynamodbtypes.ConditionalCheckFailedException](err); ok {
			existing, parseErr := jobFromItem(condErr.Item)
			return Job{}, &JobExistsError{JobID: jobID, Existing: existing, ExistingErr: parseErr}
		}
		return Job{}, fmt.Errorf("dynamo: put job %s: %w", jobID, err)
	}
	return job, nil
}
