package textract

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// StartAnalysisInput は非同期解析の起動に必要な値
//
// FeatureTypes は #34 の検証で組み合わせを差し替えるため、必ず呼び出し側が指定する
type StartAnalysisInput struct {
	Document           S3Location
	FeatureTypes       []awstextracttypes.FeatureType
	SNSTopicARN        string
	RoleARN            string
	JobTag             string
	ClientRequestToken string
	QueriesConfig      *awstextracttypes.QueriesConfig
}

// StartDocumentAnalysis は非同期解析を開始し、ジョブ ID を返す
func (c *Client) StartDocumentAnalysis(ctx context.Context, in StartAnalysisInput) (string, error) {
	if err := in.Document.validate(); err != nil {
		return "", err
	}
	if len(in.FeatureTypes) == 0 {
		return "", fmt.Errorf("%w: feature types are required", ErrInvalidInput)
	}
	if err := ValidateFeatureTypes(in.FeatureTypes); err != nil {
		return "", err
	}

	params := &awstextract.StartDocumentAnalysisInput{
		DocumentLocation: &awstextracttypes.DocumentLocation{S3Object: in.Document.toS3Object()},
		FeatureTypes:     in.FeatureTypes,
		QueriesConfig:    in.QueriesConfig,
	}
	if in.JobTag != "" {
		params.JobTag = aws.String(in.JobTag)
	}
	if in.ClientRequestToken != "" {
		params.ClientRequestToken = aws.String(in.ClientRequestToken)
	}
	if in.SNSTopicARN != "" || in.RoleARN != "" {
		if in.SNSTopicARN == "" || in.RoleARN == "" {
			return "", fmt.Errorf("%w: sns topic arn and role arn must be set together", ErrInvalidInput)
		}
		params.NotificationChannel = &awstextracttypes.NotificationChannel{
			SNSTopicArn: aws.String(in.SNSTopicARN),
			RoleArn:     aws.String(in.RoleARN),
		}
	}

	out, err := c.api.StartDocumentAnalysis(ctx, params)
	if err != nil {
		return "", fmt.Errorf("textract: start document analysis: %w", err)
	}
	if out == nil || out.JobId == nil || *out.JobId == "" {
		return "", errors.New("textract: start document analysis returned an empty job id")
	}
	return *out.JobId, nil
}

// AnalysisResult は GetDocumentAnalysis のページングを畳んだ結果
type AnalysisResult struct {
	JobID            string
	JobStatus        awstextracttypes.JobStatus
	Blocks           []awstextracttypes.Block
	DocumentMetadata *awstextracttypes.DocumentMetadata
	Warnings         []awstextracttypes.Warning
	StatusMessage    string
	ModelVersion     string
	Pages            int // Pages は取得に要したレスポンス数
}

// GetDocumentAnalysis はジョブ ID から結果を取得し、ページングを畳んで Block 配列をまとめて返す
//
// SNS の完了通知から得たジョブ ID をそのまま渡せる
func (c *Client) GetDocumentAnalysis(ctx context.Context, jobID string) (*AnalysisResult, error) {
	if jobID == "" {
		return nil, fmt.Errorf("%w: job id is required", ErrInvalidInput)
	}

	result := &AnalysisResult{JobID: jobID}
	seen := map[string]struct{}{}
	var next *string

	for {
		out, err := c.api.GetDocumentAnalysis(ctx, &awstextract.GetDocumentAnalysisInput{
			JobId:     aws.String(jobID),
			NextToken: next,
		})
		if err != nil {
			return nil, fmt.Errorf("textract: get document analysis: %w", err)
		}
		if out == nil {
			return nil, errors.New("textract: get document analysis returned no output")
		}

		result.Pages++
		result.Blocks = append(result.Blocks, out.Blocks...)
		result.Warnings = append(result.Warnings, out.Warnings...)
		if out.DocumentMetadata != nil {
			result.DocumentMetadata = out.DocumentMetadata
		}
		if out.AnalyzeDocumentModelVersion != nil {
			result.ModelVersion = *out.AnalyzeDocumentModelVersion
		}
		if out.StatusMessage != nil {
			result.StatusMessage = *out.StatusMessage
		}
		if next == nil {
			result.JobStatus = out.JobStatus
			switch out.JobStatus {
			case awstextracttypes.JobStatusInProgress:
				return result, ErrJobInProgress
			case awstextracttypes.JobStatusFailed:
				return result, fmt.Errorf("%w: %s", ErrJobFailed, result.StatusMessage)
			}
		}

		if out.NextToken == nil || *out.NextToken == "" {
			return result, nil
		}
		if _, dup := seen[*out.NextToken]; dup {
			return nil, fmt.Errorf("textract: next token %q repeated", *out.NextToken)
		}
		seen[*out.NextToken] = struct{}{}
		next = out.NextToken
	}
}
