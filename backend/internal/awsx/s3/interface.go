package s3

import (
	"context"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// API は Client が利用する SDK の操作を表す
//
// テストでフェイクに差し替えるため、SDK のクライアントを直接持たない
type API interface {
	GetObject(ctx context.Context, params *awss3.GetObjectInput, optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *awss3.PutObjectInput, optFns ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
	HeadObject(ctx context.Context, params *awss3.HeadObjectInput, optFns ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
}

var _ API = (*awss3.Client)(nil)
