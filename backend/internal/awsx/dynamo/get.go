package dynamo

import (
	"context"
	"fmt"

	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// GetJob は jobId でジョブを取得する (存在しない場合は ErrJobNotFound を返す)
func (c *Client) GetJob(ctx context.Context, jobID string) (Job, error) {
	out, err := c.api.GetItem(ctx, &awsdynamodb.GetItemInput{
		TableName: new(c.tableName),
		Key:       jobKey(jobID),
		// 冪等性の判定に使うため結果整合性の読み取りでは不十分
		ConsistentRead: new(true),
	})
	if err != nil {
		return Job{}, fmt.Errorf("dynamo: get job %s: %w", jobID, err)
	}
	if len(out.Item) == 0 {
		return Job{}, fmt.Errorf("dynamo: get job %s: %w", jobID, ErrJobNotFound)
	}
	return jobFromItem(out.Item)
}
