// Package s3 は S3 の操作を awsx 層に閉じ込める
package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ContentTypeJSON は構造化データを保存するときの Content-Type
const ContentTypeJSON = "application/json"

// ErrNotFound はオブジェクトが存在しないことを表す
//
// SDK は GetObject と HeadObject で異なるエラー型を返すため、ここで一本化する
var ErrNotFound = errors.New("s3: object not found")

// API は Client が利用する SDK の操作を表す
//
// テストでフェイクに差し替えるため、SDK のクライアントを直接持たない
type API interface {
	GetObject(ctx context.Context, params *awss3.GetObjectInput, optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *awss3.PutObjectInput, optFns ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
	HeadObject(ctx context.Context, params *awss3.HeadObjectInput, optFns ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
}

var _ API = (*awss3.Client)(nil)

// Client は awsx 層が公開する S3 クライアント
type Client struct {
	api    API
	bucket string
}

// New は SDK のクライアントをラップする
func New(api API, bucket string) *Client {
	return &Client{api: api, bucket: bucket}
}

// Bucket は操作対象のバケット名を返す
func (c *Client) Bucket() string {
	return c.bucket
}

// ObjectInfo はオブジェクトのメタデータ
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
	Metadata     map[string]string
}

// Get はオブジェクトをストリームとして取得する
//
// 500MB クラスの PDF をメモリに載せずに /tmp へ流すため、バイト列ではなく ReadCloser を返す (呼び出し側が Close する)
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.api.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
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

func wrapErr(op, key string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("s3: %s %q: %w", op, key, ErrNotFound)
	}
	return fmt.Errorf("s3: %s %q: %w", op, key, err)
}

// isNotFound は SDK が返す複数の「存在しない」表現を吸収する
//
// 同じ意味が 3 系統で来る
//   - GetObject は NoSuchKey を返す
//   - HeadObject はボディを持たないため NotFound を返す
//   - SDK が型に落とせなかった場合は 404 のまま伝わってくる
func isNotFound(err error) bool {
	if _, ok := errors.AsType[*awss3types.NoSuchKey](err); ok {
		return true
	}

	if _, ok := errors.AsType[*awss3types.NotFound](err); ok {
		return true
	}

	if respErr, ok := errors.AsType[*awshttp.ResponseError](err); ok && respErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}

	return false
}
