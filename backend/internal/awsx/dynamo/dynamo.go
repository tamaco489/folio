// Package dynamo は DynamoDB の操作を awsx 層に閉じ込める
package dynamo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// IndexStatusUpdatedAt は status ごとのジョブを更新時刻順に引く GSI
const IndexStatusUpdatedAt = "gsi-status-updatedAt"

// API は Client が利用する DynamoDB の操作。テストではフェイクに差し替える
type API interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

var _ API = (*dynamodb.Client)(nil)

// Client は awsx 層が公開する DynamoDB クライアント
type Client struct {
	api       API
	tableName string
	now       func() time.Time
}

// Option は Client の任意設定
type Option func(*Client)

// WithClock は時刻の取得方法を差し替える。テストで固定時刻を与えるために使う
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// New は SDK のクライアントをラップする
func New(api API, tableName string, opts ...Option) *Client {
	c := &Client{
		api:       api,
		tableName: tableName,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// TableName は操作対象のテーブル名を返す
func (c *Client) TableName() string { return c.tableName }

// RegisterJob は条件付き書き込みでジョブを登録する
//
// jobId はファイルの SHA-256 なので、同じ PDF の再投入は同じ jobId になる。
// 未登録か FAILED のときだけ書き込みを通すことで、専用テーブルを持たずに
// 「成功したものだけ弾く」冪等性を 1 回の書き込みで成立させる。
// 弾かれた場合は JobExistsError に旧レコードを載せて返すので、
// 呼び出し側は追加の読み取りなしに PROCESSING か COMPLETED かを判定できる。
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

	_, err = c.api.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(#jobId) OR #status = :failed"),
		ExpressionAttributeNames: map[string]string{
			"#jobId":  attrJobID,
			"#status": attrStatus,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":failed": &types.AttributeValueMemberS{Value: string(StatusFailed)},
		},
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			existing, parseErr := jobFromItem(condErr.Item)
			return Job{}, &JobExistsError{JobID: jobID, Existing: existing, ExistingErr: parseErr}
		}
		return Job{}, fmt.Errorf("dynamo: put job %s: %w", jobID, err)
	}
	return job, nil
}

// GetJob は jobId でジョブを取得する。存在しない場合は ErrJobNotFound を返す
func (c *Client) GetJob(ctx context.Context, jobID string) (Job, error) {
	out, err := c.api.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key:       jobKey(jobID),
		// 冪等性の判定に使うため結果整合性の読み取りでは不十分
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return Job{}, fmt.Errorf("dynamo: get job %s: %w", jobID, err)
	}
	if len(out.Item) == 0 {
		return Job{}, fmt.Errorf("dynamo: get job %s: %w", jobID, ErrJobNotFound)
	}
	return jobFromItem(out.Item)
}

// UpdateStatus は既存ジョブの status を更新し、残っている errorReason を消す
//
// FAILED は理由の記録が必須のため受け付けない。MarkFailed を使う
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
		map[string]types.AttributeValue{
			":status":    &types.AttributeValueMemberS{Value: string(status)},
			":updatedAt": &types.AttributeValueMemberS{Value: formatTime(c.now())},
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
		map[string]types.AttributeValue{
			":status":      &types.AttributeValueMemberS{Value: string(StatusFailed)},
			":updatedAt":   &types.AttributeValueMemberS{Value: formatTime(c.now())},
			":errorReason": &types.AttributeValueMemberS{Value: reason},
		},
	)
}

func (c *Client) updateItem(ctx context.Context, jobID, expr string, names map[string]string, values map[string]types.AttributeValue) (Job, error) {
	out, err := c.api.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(c.tableName),
		Key:                       jobKey(jobID),
		UpdateExpression:          aws.String(expr),
		ConditionExpression:       aws.String("attribute_exists(#jobId)"),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ReturnValues:              types.ReturnValueAllNew,
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return Job{}, fmt.Errorf("dynamo: update job %s: %w", jobID, ErrJobNotFound)
		}
		return Job{}, fmt.Errorf("dynamo: update job %s: %w", jobID, err)
	}
	return jobFromItem(out.Attributes)
}

// ListByStatus は GSI で同じ status のジョブを updatedAt の新しい順に取得する
// limit が 0 以下の場合は DynamoDB の 1 ページ分をそのまま返す
func (c *Client) ListByStatus(ctx context.Context, status Status, limit int32) ([]Job, error) {
	if !status.Valid() {
		return nil, fmt.Errorf("dynamo: unknown status %q", status)
	}
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		IndexName:              aws.String(IndexStatusUpdatedAt),
		KeyConditionExpression: aws.String("#status = :status"),
		ExpressionAttributeNames: map[string]string{
			"#status": attrStatus,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: string(status)},
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

func jobKey(jobID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		attrJobID: &types.AttributeValueMemberS{Value: jobID},
	}
}
