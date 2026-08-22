package textract

import (
	"fmt"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// S3Location は Textract に渡す S3 上のオブジェクト
type S3Location struct {
	Bucket string
	Key    string
}

func (l S3Location) toS3Object() *awstextracttypes.S3Object {
	return &awstextracttypes.S3Object{
		Bucket: new(l.Bucket),
		Name:   new(l.Key),
	}
}

func (l S3Location) validate() error {
	if l.Bucket == "" || l.Key == "" {
		return fmt.Errorf("%w: bucket and key are required", ErrInvalidInput)
	}
	return nil
}
