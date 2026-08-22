package textract_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/textract"
)

// fakeAPI は実 API を呼ばずに任意のレスポンスを返すテスト用の実装
type fakeAPI struct {
	start        func(*awstextract.StartDocumentAnalysisInput) (*awstextract.StartDocumentAnalysisOutput, error)
	get          func(*awstextract.GetDocumentAnalysisInput) (*awstextract.GetDocumentAnalysisOutput, error)
	detect       func(*awstextract.DetectDocumentTextInput) (*awstextract.DetectDocumentTextOutput, error)
	startInputs  []*awstextract.StartDocumentAnalysisInput
	getInputs    []*awstextract.GetDocumentAnalysisInput
	detectInputs []*awstextract.DetectDocumentTextInput
}

func (f *fakeAPI) StartDocumentAnalysis(_ context.Context, in *awstextract.StartDocumentAnalysisInput, _ ...func(*awstextract.Options)) (*awstextract.StartDocumentAnalysisOutput, error) {
	f.startInputs = append(f.startInputs, in)
	if f.start == nil {
		return &awstextract.StartDocumentAnalysisOutput{JobId: new("job-1")}, nil
	}
	return f.start(in)
}

func (f *fakeAPI) GetDocumentAnalysis(_ context.Context, in *awstextract.GetDocumentAnalysisInput, _ ...func(*awstextract.Options)) (*awstextract.GetDocumentAnalysisOutput, error) {
	f.getInputs = append(f.getInputs, in)
	return f.get(in)
}

func (f *fakeAPI) DetectDocumentText(_ context.Context, in *awstextract.DetectDocumentTextInput, _ ...func(*awstextract.Options)) (*awstextract.DetectDocumentTextOutput, error) {
	f.detectInputs = append(f.detectInputs, in)
	if f.detect == nil {
		return &awstextract.DetectDocumentTextOutput{}, nil
	}
	return f.detect(in)
}

func TestStartDocumentAnalysisPassesFeatureTypes(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	c := textract.New(api)

	jobID, err := c.StartDocumentAnalysis(context.Background(), textract.StartAnalysisInput{
		Document:     textract.S3Location{Bucket: "folio-dev", Key: "uploads/job-1/original.pdf"},
		FeatureTypes: []awstextracttypes.FeatureType{awstextracttypes.FeatureTypeLayout, awstextracttypes.FeatureTypeTables},
		SNSTopicARN:  "arn:aws:sns:ap-northeast-1:000000000000:dev-folio-textract",
		RoleARN:      "arn:aws:iam::000000000000:role/dev-folio-textract-sns",
		JobTag:       "1706.03762",
	})
	if err != nil {
		t.Fatalf("StartDocumentAnalysis: %v", err)
	}
	if jobID != "job-1" {
		t.Fatalf("job id = %q, want job-1", jobID)
	}

	in := api.startInputs[0]
	if got, want := len(in.FeatureTypes), 2; got != want {
		t.Fatalf("feature types = %d, want %d", got, want)
	}
	if in.FeatureTypes[0] != awstextracttypes.FeatureTypeLayout || in.FeatureTypes[1] != awstextracttypes.FeatureTypeTables {
		t.Fatalf("feature types = %v", in.FeatureTypes)
	}
	if in.NotificationChannel == nil || aws.ToString(in.NotificationChannel.SNSTopicArn) == "" {
		t.Fatal("notification channel is not set")
	}
	if aws.ToString(in.DocumentLocation.S3Object.Name) != "uploads/job-1/original.pdf" {
		t.Fatalf("s3 key = %q", aws.ToString(in.DocumentLocation.S3Object.Name))
	}
}

func TestStartDocumentAnalysisRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]textract.StartAnalysisInput{
		"異常系_バケットが未指定の場合_ErrInvalidInput が返ること": {
			Document:     textract.S3Location{Key: "uploads/job-1/original.pdf"},
			FeatureTypes: []awstextracttypes.FeatureType{awstextracttypes.FeatureTypeLayout},
		},
		"異常系_FeatureTypes が未指定の場合_ErrInvalidInput が返ること": {
			Document: textract.S3Location{Bucket: "folio-dev", Key: "uploads/job-1/original.pdf"},
		},
		"異常系_未知の FeatureType の場合_ErrInvalidInput が返ること": {
			Document:     textract.S3Location{Bucket: "folio-dev", Key: "uploads/job-1/original.pdf"},
			FeatureTypes: []awstextracttypes.FeatureType{"LAYOUTS"},
		},
		"異常系_SNS トピックのみ指定した場合_ErrInvalidInput が返ること": {
			Document:     textract.S3Location{Bucket: "folio-dev", Key: "uploads/job-1/original.pdf"},
			FeatureTypes: []awstextracttypes.FeatureType{awstextracttypes.FeatureTypeLayout},
			SNSTopicARN:  "arn:aws:sns:ap-northeast-1:000000000000:dev-folio-textract",
		},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := &fakeAPI{}
			if _, err := textract.New(api).StartDocumentAnalysis(context.Background(), in); !errors.Is(err, textract.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if len(api.startInputs) != 0 {
				t.Fatal("api was called despite invalid input")
			}
		})
	}
}

func TestGetDocumentAnalysisInProgress(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		get: func(*awstextract.GetDocumentAnalysisInput) (*awstextract.GetDocumentAnalysisOutput, error) {
			return &awstextract.GetDocumentAnalysisOutput{JobStatus: awstextracttypes.JobStatusInProgress}, nil
		},
	}

	res, err := textract.New(api).GetDocumentAnalysis(context.Background(), "job-1")
	if !errors.Is(err, textract.ErrJobInProgress) {
		t.Fatalf("err = %v, want ErrJobInProgress", err)
	}
	if res.JobStatus != awstextracttypes.JobStatusInProgress {
		t.Fatalf("job status = %q", res.JobStatus)
	}
	if len(api.getInputs) != 1 {
		t.Fatalf("api calls = %d, want 1", len(api.getInputs))
	}
}

func TestGetDocumentAnalysisFailed(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		get: func(*awstextract.GetDocumentAnalysisInput) (*awstextract.GetDocumentAnalysisOutput, error) {
			return &awstextract.GetDocumentAnalysisOutput{
				JobStatus:     awstextracttypes.JobStatusFailed,
				StatusMessage: new("document is password protected"),
			}, nil
		},
	}

	res, err := textract.New(api).GetDocumentAnalysis(context.Background(), "job-1")
	if !errors.Is(err, textract.ErrJobFailed) {
		t.Fatalf("err = %v, want ErrJobFailed", err)
	}
	if res.StatusMessage != "document is password protected" {
		t.Fatalf("status message = %q", res.StatusMessage)
	}
}

func TestGetDocumentAnalysisDetectsRepeatedToken(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		get: func(*awstextract.GetDocumentAnalysisInput) (*awstextract.GetDocumentAnalysisOutput, error) {
			return &awstextract.GetDocumentAnalysisOutput{
				JobStatus: awstextracttypes.JobStatusSucceeded,
				NextToken: new("same"),
			}, nil
		},
	}

	if _, err := textract.New(api).GetDocumentAnalysis(context.Background(), "job-1"); err == nil {
		t.Fatal("err = nil, want an error for the repeated token")
	}
}

func TestGetDocumentAnalysisRequiresJobID(t *testing.T) {
	t.Parallel()

	if _, err := textract.New(&fakeAPI{}).GetDocumentAnalysis(context.Background(), ""); !errors.Is(err, textract.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDetectDocumentTextUsesBytesWhenGiven(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		detect: func(*awstextract.DetectDocumentTextInput) (*awstextract.DetectDocumentTextOutput, error) {
			return &awstextract.DetectDocumentTextOutput{
				Blocks:                         []awstextracttypes.Block{{BlockType: awstextracttypes.BlockTypeLine, Text: new("hello")}},
				DetectDocumentTextModelVersion: new("1.0"),
			}, nil
		},
	}

	res, err := textract.New(api).DetectDocumentText(context.Background(), textract.DetectInput{Bytes: []byte("png")})
	if err != nil {
		t.Fatalf("DetectDocumentText: %v", err)
	}
	if res.ModelVersion != "1.0" || len(res.Blocks) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if api.detectInputs[0].Document.S3Object != nil {
		t.Fatal("s3 object is set even though bytes were given")
	}
}

func TestDetectDocumentTextUsesS3(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	_, err := textract.New(api).DetectDocumentText(context.Background(), textract.DetectInput{
		Document: textract.S3Location{Bucket: "folio-dev", Key: "work/job-1/pages/page-001.png"},
	})
	if err != nil {
		t.Fatalf("DetectDocumentText: %v", err)
	}
	if aws.ToString(api.detectInputs[0].Document.S3Object.Name) != "work/job-1/pages/page-001.png" {
		t.Fatalf("s3 key = %q", aws.ToString(api.detectInputs[0].Document.S3Object.Name))
	}
}

func TestParseFeatureTypes(t *testing.T) {
	t.Parallel()

	got, err := textract.ParseFeatureTypes([]string{"LAYOUT", "TABLES"})
	if err != nil {
		t.Fatalf("ParseFeatureTypes: %v", err)
	}
	if len(got) != 2 || got[0] != awstextracttypes.FeatureTypeLayout || got[1] != awstextracttypes.FeatureTypeTables {
		t.Fatalf("features = %v", got)
	}

	for _, in := range [][]string{nil, {"LAYOUT", "LAYOUT"}, {"UNKNOWN"}} {
		if _, err := textract.ParseFeatureTypes(in); !errors.Is(err, textract.ErrInvalidInput) {
			t.Fatalf("ParseFeatureTypes(%v) err = %v, want ErrInvalidInput", in, err)
		}
	}
}
