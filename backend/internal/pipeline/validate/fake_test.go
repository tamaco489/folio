package validate

import (
	"context"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

var _ PDFInfo = (*fakePDF)(nil)

// fakePDF は poppler を呼ばずに pdfinfo の結果を差し替える
type fakePDF struct {
	info pdf.Info
	err  error
}

func (f *fakePDF) Info(context.Context, string) (pdf.Info, error) {
	return f.info, f.err
}

var _ s3.API = (*oversizeS3)(nil)

// oversizeS3 は HeadObject が報告するサイズだけを差し替える
//
// 500MB の実データを用意せずに上限超過を検証するために使う
type oversizeS3 struct {
	s3.API
	size int64
}

func (o *oversizeS3) HeadObject(ctx context.Context, params *awss3.HeadObjectInput, optFns ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	out, err := o.API.HeadObject(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	out.ContentLength = new(o.size)
	return out, nil
}
