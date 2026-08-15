// Package textract は Textract の操作を awsx 層に閉じ込める
package textract

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// API は本パッケージが使う Textract の操作だけを切り出したインターフェース
//
// *awstextract.Client のほか Recorder と Replayer がこれを満たす
type API interface {
	StartDocumentAnalysis(ctx context.Context, params *awstextract.StartDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.StartDocumentAnalysisOutput, error)
	GetDocumentAnalysis(ctx context.Context, params *awstextract.GetDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.GetDocumentAnalysisOutput, error)
	DetectDocumentText(ctx context.Context, params *awstextract.DetectDocumentTextInput, optFns ...func(*awstextract.Options)) (*awstextract.DetectDocumentTextOutput, error)
}

var (
	// ErrJobInProgress はジョブが未完了であることを示す
	ErrJobInProgress = errors.New("textract: job is in progress")

	// ErrJobFailed はジョブが失敗したことを示す
	ErrJobFailed = errors.New("textract: job failed")

	// ErrInvalidInput は呼び出し側の引数不備を示す
	ErrInvalidInput = errors.New("textract: invalid input")
)

// Client は awsx 層が公開する Textract クライアント
type Client struct {
	api API
}

// New は SDK のクライアントをラップする
func New(api API) *Client {
	return &Client{api: api}
}

// S3Location は Textract に渡す S3 上のオブジェクト
type S3Location struct {
	Bucket string
	Key    string
}

func (l S3Location) toS3Object() *types.S3Object {
	return &types.S3Object{
		Bucket: aws.String(l.Bucket),
		Name:   aws.String(l.Key),
	}
}

func (l S3Location) validate() error {
	if l.Bucket == "" || l.Key == "" {
		return fmt.Errorf("%w: bucket and key are required", ErrInvalidInput)
	}
	return nil
}

// StartAnalysisInput は非同期解析の起動に必要な値
//
// FeatureTypes は #34 の検証で組み合わせを差し替えるため、必ず呼び出し側が指定する
type StartAnalysisInput struct {
	Document           S3Location
	FeatureTypes       []types.FeatureType
	SNSTopicARN        string
	RoleARN            string
	JobTag             string
	ClientRequestToken string
	QueriesConfig      *types.QueriesConfig
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
		DocumentLocation: &types.DocumentLocation{S3Object: in.Document.toS3Object()},
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
		params.NotificationChannel = &types.NotificationChannel{
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
	JobStatus        types.JobStatus
	Blocks           []types.Block
	DocumentMetadata *types.DocumentMetadata
	Warnings         []types.Warning
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
			case types.JobStatusInProgress:
				return result, ErrJobInProgress
			case types.JobStatusFailed:
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

// DetectInput は DetectDocumentText の入力
//
// Bytes を指定した場合は S3 ではなくバイト列を送る
type DetectInput struct {
	Document S3Location
	Bytes    []byte
}

// DetectResult は DetectDocumentText の結果
type DetectResult struct {
	Blocks           []types.Block
	DocumentMetadata *types.DocumentMetadata
	ModelVersion     string
}

// DetectDocumentText は OCR のみの同期 API を呼ぶ
//
// #34 で FeatureTypes 付きの解析と比較するベースラインとして使う
func (c *Client) DetectDocumentText(ctx context.Context, in DetectInput) (*DetectResult, error) {
	doc := &types.Document{}
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

// ParseFeatureTypes は文字列の並びを FeatureType に変換する
//
// 組み合わせは未確定のため、環境変数や設定値から渡せるようにしている
func ParseFeatureTypes(values []string) ([]types.FeatureType, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: feature types are required", ErrInvalidInput)
	}
	features := make([]types.FeatureType, 0, len(values))
	for _, v := range values {
		features = append(features, types.FeatureType(v))
	}
	if err := ValidateFeatureTypes(features); err != nil {
		return nil, err
	}
	return features, nil
}

// ValidateFeatureTypes は SDK が定義する列挙値であるかと重複がないかを検査する
func ValidateFeatureTypes(features []types.FeatureType) error {
	known := map[types.FeatureType]struct{}{}
	for _, v := range types.FeatureType("").Values() {
		known[v] = struct{}{}
	}
	seen := map[types.FeatureType]struct{}{}
	for _, f := range features {
		if _, ok := known[f]; !ok {
			return fmt.Errorf("%w: unknown feature type %q", ErrInvalidInput, string(f))
		}
		if _, dup := seen[f]; dup {
			return fmt.Errorf("%w: duplicated feature type %q", ErrInvalidInput, string(f))
		}
		seen[f] = struct{}{}
	}
	return nil
}
