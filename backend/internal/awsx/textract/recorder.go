package textract

import (
	"context"
	"errors"
	"sync"
	"time"

	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// Recorder は実 API の呼び出しを通しつつレスポンスを記録する
//
// 記録の取得には課金が発生するため、実行はユーザーの承認を得た場合に限る
type Recorder struct {
	inner API
	mu    sync.Mutex
	rec   Recording
	now   func() time.Time
}

var _ API = (*Recorder)(nil)

// NewRecorder は記録モードの API を返す
func NewRecorder(inner API, paperID string) *Recorder {
	return &Recorder{
		inner: inner,
		rec:   Recording{PaperID: paperID},
		now:   time.Now,
	}
}

// Recording はここまでに記録した内容の複製を返す
func (r *Recorder) Recording() *Recording {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.rec
	out.AnalysisPages = append([]AnalysisPage(nil), r.rec.AnalysisPages...)
	return &out
}

// WriteFile は記録を論文 ID のファイルへ書き出す
func (r *Recorder) WriteFile(dir string) error {
	rec := r.Recording()
	if rec.PaperID == "" {
		return errors.New("textract: recorder has no paper id")
	}
	return rec.WriteFile(RecordingPath(dir, rec.PaperID))
}

// StartDocumentAnalysis は実 API を呼び、ジョブ ID と FeatureTypes を記録する
func (r *Recorder) StartDocumentAnalysis(ctx context.Context, params *awstextract.StartDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.StartDocumentAnalysisOutput, error) {
	out, err := r.inner.StartDocumentAnalysis(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.RecordedAt = r.now().UTC()
	if params != nil {
		r.rec.FeatureTypes = params.FeatureTypes
	}
	if out != nil && out.JobId != nil {
		r.rec.JobID = *out.JobId
	}
	return out, nil
}

// GetDocumentAnalysis は実 API を呼び、ページングの順にレスポンスを記録する
func (r *Recorder) GetDocumentAnalysis(ctx context.Context, params *awstextract.GetDocumentAnalysisInput, optFns ...func(*awstextract.Options)) (*awstextract.GetDocumentAnalysisOutput, error) {
	out, err := r.inner.GetDocumentAnalysis(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return out, nil
	}

	page := AnalysisPage{
		JobStatus:        out.JobStatus,
		Blocks:           out.Blocks,
		DocumentMetadata: out.DocumentMetadata,
		Warnings:         out.Warnings,
	}
	if out.NextToken != nil {
		page.NextToken = *out.NextToken
	}
	if out.StatusMessage != nil {
		page.StatusMessage = *out.StatusMessage
	}
	if out.AnalyzeDocumentModelVersion != nil {
		page.ModelVersion = *out.AnalyzeDocumentModelVersion
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec.RecordedAt.IsZero() {
		r.rec.RecordedAt = r.now().UTC()
	}
	if r.rec.JobID == "" && params != nil && params.JobId != nil {
		r.rec.JobID = *params.JobId
	}
	// 未完了のレスポンスを記録すると再生時に完了まで進めないため捨てる
	if out.JobStatus == awstextracttypes.JobStatusInProgress {
		return out, nil
	}
	if params == nil || params.NextToken == nil || *params.NextToken == "" {
		r.rec.AnalysisPages = nil
	}
	r.rec.AnalysisPages = append(r.rec.AnalysisPages, page)
	return out, nil
}

// DetectDocumentText は実 API を呼び、レスポンスを記録する
func (r *Recorder) DetectDocumentText(ctx context.Context, params *awstextract.DetectDocumentTextInput, optFns ...func(*awstextract.Options)) (*awstextract.DetectDocumentTextOutput, error) {
	out, err := r.inner.DetectDocumentText(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return out, nil
	}

	page := &DetectPage{
		Blocks:           out.Blocks,
		DocumentMetadata: out.DocumentMetadata,
	}
	if out.DetectDocumentTextModelVersion != nil {
		page.ModelVersion = *out.DetectDocumentTextModelVersion
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec.RecordedAt.IsZero() {
		r.rec.RecordedAt = r.now().UTC()
	}
	r.rec.Detect = page
	return out, nil
}
