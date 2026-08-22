package textract

import (
	"context"
	"errors"
	"fmt"

	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
)

// Replayer は記録済みレスポンスを返す API 実装
//
// 実 API を呼ばずに Client のページング処理まで含めて検証できる
type Replayer struct {
	rec    *Recording
	byNext map[string]int
}

var _ API = (*Replayer)(nil)

// NewReplayer は記録を再生する API を返す
func NewReplayer(rec *Recording) (*Replayer, error) {
	if rec == nil {
		return nil, errors.New("textract: recording is nil")
	}
	if err := rec.Validate(); err != nil {
		return nil, fmt.Errorf("textract: %w", err)
	}
	byNext := make(map[string]int, len(rec.AnalysisPages))
	for i, p := range rec.AnalysisPages {
		if p.NextToken != "" {
			byNext[p.NextToken] = i + 1
		}
	}
	return &Replayer{rec: rec, byNext: byNext}, nil
}

// StartDocumentAnalysis は記録に残したジョブ ID を返す
func (r *Replayer) StartDocumentAnalysis(_ context.Context, params *awstextract.StartDocumentAnalysisInput, _ ...func(*awstextract.Options)) (*awstextract.StartDocumentAnalysisOutput, error) {
	if params == nil || params.DocumentLocation == nil {
		return nil, errors.New("textract replay: document location is required")
	}
	if r.rec.JobID == "" {
		return nil, errors.New("textract replay: recording has no job id")
	}
	return &awstextract.StartDocumentAnalysisOutput{JobId: new(r.rec.JobID)}, nil
}

// GetDocumentAnalysis は NextToken に対応する記録済みページを返す
func (r *Replayer) GetDocumentAnalysis(_ context.Context, params *awstextract.GetDocumentAnalysisInput, _ ...func(*awstextract.Options)) (*awstextract.GetDocumentAnalysisOutput, error) {
	if params == nil || params.JobId == nil || *params.JobId == "" {
		return nil, errors.New("textract replay: job id is required")
	}
	if r.rec.JobID != "" && *params.JobId != r.rec.JobID {
		return nil, fmt.Errorf("textract replay: unknown job id %q", *params.JobId)
	}
	if len(r.rec.AnalysisPages) == 0 {
		return nil, errors.New("textract replay: recording has no analysis pages")
	}

	idx := 0
	if params.NextToken != nil && *params.NextToken != "" {
		i, ok := r.byNext[*params.NextToken]
		if !ok {
			return nil, fmt.Errorf("textract replay: unknown next token %q", *params.NextToken)
		}
		idx = i
	}

	p := r.rec.AnalysisPages[idx]
	out := &awstextract.GetDocumentAnalysisOutput{
		JobStatus:        p.JobStatus,
		Blocks:           p.Blocks,
		DocumentMetadata: p.DocumentMetadata,
		Warnings:         p.Warnings,
	}
	if p.NextToken != "" {
		token := p.NextToken
		out.NextToken = &token
	}
	if p.StatusMessage != "" {
		msg := p.StatusMessage
		out.StatusMessage = &msg
	}
	if p.ModelVersion != "" {
		v := p.ModelVersion
		out.AnalyzeDocumentModelVersion = &v
	}
	return out, nil
}

// DetectDocumentText は記録済みの OCR 結果を返す
func (r *Replayer) DetectDocumentText(_ context.Context, _ *awstextract.DetectDocumentTextInput, _ ...func(*awstextract.Options)) (*awstextract.DetectDocumentTextOutput, error) {
	if r.rec.Detect == nil {
		return nil, errors.New("textract replay: recording has no detect result")
	}
	out := &awstextract.DetectDocumentTextOutput{
		Blocks:           r.rec.Detect.Blocks,
		DocumentMetadata: r.rec.Detect.DocumentMetadata,
	}
	if r.rec.Detect.ModelVersion != "" {
		v := r.rec.Detect.ModelVersion
		out.DetectDocumentTextModelVersion = &v
	}
	return out, nil
}
