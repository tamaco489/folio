package s3

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectInfo はオブジェクトのメタデータ
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
	Metadata     map[string]string
}

// Head はオブジェクトのメタデータを取得する
func (c *Client) Head(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := c.api.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, wrapErr("head object", key, err)
	}

	info := ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		Metadata:     out.Metadata,
	}
	return info, nil
}

// Exists はオブジェクトの存在を確認する
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if _, err := c.Head(ctx, key); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
