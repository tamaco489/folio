package textract

import (
	"context"
	"errors"
	"fmt"

	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// DetectInput は DetectDocumentText の入力
//
// Bytes を指定した場合は S3 ではなくバイト列を送る
type DetectInput struct {
	Document S3Location
	Bytes    []byte
}

// DetectResult は DetectDocumentText の結果
type DetectResult struct {
	Blocks           []awstextracttypes.Block
	DocumentMetadata *awstextracttypes.DocumentMetadata
	ModelVersion     string
}

// DetectDocumentText は OCR のみの同期 API を呼ぶ
//
// #34 で FeatureTypes 付きの解析と比較するベースラインとして使う
func (c *Client) DetectDocumentText(ctx context.Context, in DetectInput) (*DetectResult, error) {
	doc := &awstextracttypes.Document{}
	if len(in.Bytes) > 0 {
		doc.Bytes = in.Bytes
	} else {
		if err := in.Document.validate(); err != nil {
			return nil, err
		}
		doc.S3Object = in.Document.toS3Object()
	}

	out, err := c.api.DetectDocumentText(ctx, &awstextract.DetectDocumentTextInput{Document: doc})
	if err != nil {
		return nil, fmt.Errorf("textract: detect document text: %w", err)
	}
	if out == nil {
		return nil, errors.New("textract: detect document text returned no output")
	}

	res := &DetectResult{
		Blocks:           out.Blocks,
		DocumentMetadata: out.DocumentMetadata,
	}
	if out.DetectDocumentTextModelVersion != nil {
		res.ModelVersion = *out.DetectDocumentTextModelVersion
	}
	return res, nil
}
