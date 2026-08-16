package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// PutOption は保存時の付加情報を指定する
type PutOption func(*awss3.PutObjectInput)

// WithContentType は Content-Type を指定する
func WithContentType(contentType string) PutOption {
	return func(in *awss3.PutObjectInput) {
		in.ContentType = aws.String(contentType)
	}
}

// WithContentLength は Content-Length を指定する
//
// SDK は seek できない io.Reader を渡されるとサイズを判定できないため、Put でストリームを渡す場合は明示する
func WithContentLength(size int64) PutOption {
	return func(in *awss3.PutObjectInput) {
		in.ContentLength = aws.Int64(size)
	}
}

// WithMetadata はユーザ定義メタデータを付与する
func WithMetadata(metadata map[string]string) PutOption {
	return func(in *awss3.PutObjectInput) {
		in.Metadata = metadata
	}
}

// Put はストリームをオブジェクトとして保存する
func (c *Client) Put(ctx context.Context, key string, body io.Reader, opts ...PutOption) error {
	in := &awss3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	for _, opt := range opts {
		opt(in)
	}
	if _, err := c.api.PutObject(ctx, in); err != nil {
		return wrapErr("put object", key, err)
	}
	return nil
}

// PutBytes はバイト列をオブジェクトとして保存する
func (c *Client) PutBytes(ctx context.Context, key string, body []byte, opts ...PutOption) error {
	opts = append([]PutOption{WithContentLength(int64(len(body)))}, opts...)
	return c.Put(ctx, key, bytes.NewReader(body), opts...)
}

// PutJSON は v を JSON として保存する
func (c *Client) PutJSON(ctx context.Context, key string, v any, opts ...PutOption) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("s3: encode object %q: %w", key, err)
	}
	opts = append([]PutOption{WithContentType(ContentTypeJSON)}, opts...)
	return c.PutBytes(ctx, key, b, opts...)
}
