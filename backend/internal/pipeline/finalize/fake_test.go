package finalize

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
)

var _ s3.API = (*spyS3)(nil)

// spyS3 は s3test.Fake に委譲しつつ GetObject のキーを記録する (読みに行かないことを確かめるため)
type spyS3 struct {
	*s3test.Fake
	gets []string
}

func (s *spyS3) GetObject(ctx context.Context, params *awss3.GetObjectInput, optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	s.gets = append(s.gets, aws.ToString(params.Key))
	return s.Fake.GetObject(ctx, params, optFns...)
}
