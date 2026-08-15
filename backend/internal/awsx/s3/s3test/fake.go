// Package s3test は実 API を呼ばずに awsx/s3 を検証するためのフェイクを提供する
package s3test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
)

var _ s3.API = (*Fake)(nil)

// Object はフェイクが保持するオブジェクト
type Object struct {
	Body         []byte
	ContentType  string
	Metadata     map[string]string
	LastModified time.Time
}

// Fake はメモリ上に S3 を再現する
//
// GetErr / PutErr / HeadErr に値を入れると、その操作を失敗させられる
type Fake struct {
	mu      sync.Mutex
	objects map[string]Object

	GetErr  error
	PutErr  error
	HeadErr error
}

// NewFake は空の Fake を生成する
func NewFake() *Fake {
	return &Fake{objects: make(map[string]Object)}
}

// Seed はテストの前提となるオブジェクトを配置する
func (f *Fake) Seed(bucket, key string, obj Object) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[bucket+"/"+key] = obj
}

// Object は配置されているオブジェクトを取り出す
func (f *Fake) Object(bucket, key string) (Object, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[bucket+"/"+key]
	return obj, ok
}

func (f *Fake) GetObject(_ context.Context, params *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	if f.GetErr != nil {
		return nil, f.GetErr
	}

	obj, ok := f.Object(aws.ToString(params.Bucket), aws.ToString(params.Key))
	if !ok {
		return nil, &types.NoSuchKey{}
	}

	return &awss3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(obj.Body)),
		ContentLength: aws.Int64(int64(len(obj.Body))),
		ContentType:   aws.String(obj.ContentType),
		Metadata:      obj.Metadata,
	}, nil
}

func (f *Fake) PutObject(_ context.Context, params *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	if f.PutErr != nil {
		return nil, f.PutErr
	}

	var body []byte
	if params.Body != nil {
		b, err := io.ReadAll(params.Body)
		if err != nil {
			return nil, err
		}
		body = b
	}

	f.Seed(aws.ToString(params.Bucket), aws.ToString(params.Key), Object{
		Body:         body,
		ContentType:  aws.ToString(params.ContentType),
		Metadata:     params.Metadata,
		LastModified: time.Now().UTC(),
	})
	return &awss3.PutObjectOutput{}, nil
}

func (f *Fake) HeadObject(_ context.Context, params *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	if f.HeadErr != nil {
		return nil, f.HeadErr
	}

	obj, ok := f.Object(aws.ToString(params.Bucket), aws.ToString(params.Key))
	if !ok {
		return nil, &types.NotFound{}
	}

	return &awss3.HeadObjectOutput{
		ContentLength: aws.Int64(int64(len(obj.Body))),
		ContentType:   aws.String(obj.ContentType),
		ETag:          aws.String(`"fake-etag"`),
		LastModified:  aws.Time(obj.LastModified),
		Metadata:      obj.Metadata,
	}, nil
}
