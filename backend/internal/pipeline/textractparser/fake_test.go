package textractparser

import (
	"context"

	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"

	"github.com/tamaco489/folio/backend/internal/awsx/sfn"
	"github.com/tamaco489/folio/backend/internal/awsx/textract"
)

// fakeStates は Step Functions への応答を記録する sfn.API のフェイク (実 API は一切呼ばない)
type fakeStates struct {
	successes []*awssfn.SendTaskSuccessInput
	failures  []*awssfn.SendTaskFailureInput
	err       error
}

var _ sfn.API = (*fakeStates)(nil)

func (f *fakeStates) SendTaskSuccess(_ context.Context, params *awssfn.SendTaskSuccessInput, _ ...func(*awssfn.Options)) (*awssfn.SendTaskSuccessOutput, error) {
	f.successes = append(f.successes, params)
	if f.err != nil {
		return nil, f.err
	}
	return &awssfn.SendTaskSuccessOutput{}, nil
}

func (f *fakeStates) SendTaskFailure(_ context.Context, params *awssfn.SendTaskFailureInput, _ ...func(*awssfn.Options)) (*awssfn.SendTaskFailureOutput, error) {
	f.failures = append(f.failures, params)
	if f.err != nil {
		return nil, f.err
	}
	return &awssfn.SendTaskFailureOutput{}, nil
}

// spyTextract は Replayer に委譲しつつ StartDocumentAnalysis の入力を記録し、任意で開始と取得を失敗させる
//
// Replayer は開始の引数を見ないため、NotificationChannel と JobTag が渡っていることはここで確かめる
type spyTextract struct {
	textract.API
	startInputs []*awstextract.StartDocumentAnalysisInput
	startErr    error
	getErr      error
}

var _ textract.API = (*spyTextract)(nil)

func (s *spyTextract) StartDocumentAnalysis(ctx context.Context, params *awstextract.StartDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.StartDocumentAnalysisOutput, error) {
	s.startInputs = append(s.startInputs, params)
	if s.startErr != nil {
		return nil, s.startErr
	}
	return s.API.StartDocumentAnalysis(ctx, params, optFns...)
}

func (s *spyTextract) GetDocumentAnalysis(ctx context.Context, params *awstextract.GetDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.GetDocumentAnalysisOutput, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.API.GetDocumentAnalysis(ctx, params, optFns...)
}
