package s3

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// Get はオブジェクトをストリームとして取得する
//
// 500MB クラスの PDF をメモリに載せずに /tmp へ流すため、バイト列ではなく ReadCloser を返す (呼び出し側が Close する)
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.api.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: new(c.bucket),
		Key:    new(key),
	})
	if err != nil {
		return nil, wrapErr("get object", key, err)
	}
	return out.Body, nil
}

// GetBytes はオブジェクト全体をメモリに読み込む
//
// JSON など小さいオブジェクト向け (PDF には Get または Download を使う)
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	body, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	b, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("s3: read object %q: %w", key, err)
	}
	return b, nil
}

// Download はオブジェクトを dst へ書き出し、書き込んだバイト数を返す
func (c *Client) Download(ctx context.Context, key string, dst io.Writer) (int64, error) {
	body, err := c.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	defer body.Close()

	n, err := io.Copy(dst, body)
	if err != nil {
		return n, fmt.Errorf("s3: download object %q: %w", key, err)
	}
	return n, nil
}

// GetJSON はオブジェクトを JSON として v にデコードする
func (c *Client) GetJSON(ctx context.Context, key string, v any) error {
	b, err := c.GetBytes(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("s3: decode object %q: %w", key, err)
	}
	return nil
}
