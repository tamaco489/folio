package textract

import (
	"context"

	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
)

// API は本パッケージが使う Textract の操作だけを切り出したインターフェース
//
// *awstextract.Client のほか Recorder と Replayer がこれを満たす
type API interface {
	StartDocumentAnalysis(ctx context.Context, params *awstextract.StartDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.StartDocumentAnalysisOutput, error)
	GetDocumentAnalysis(ctx context.Context, params *awstextract.GetDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.GetDocumentAnalysisOutput, error)
	DetectDocumentText(ctx context.Context, params *awstextract.DetectDocumentTextInput, optFns ...func(*awstextract.Options)) (*awstextract.DetectDocumentTextOutput, error)
}

var _ API = (*awstextract.Client)(nil)
