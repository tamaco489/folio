package textract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// Recording は 1 本の論文に対する Textract の記録済みレスポンス
//
// 配置は testdata/textract/{論文 ID}.json
type Recording struct {
	PaperID       string              `json:"paperId"`
	JobID         string              `json:"jobId,omitempty"`
	FeatureTypes  []types.FeatureType `json:"featureTypes,omitempty"`
	RecordedAt    time.Time           `json:"recordedAt,omitempty"`
	Note          string              `json:"note,omitempty"`          // Note は記録の出自を残すための備考で、手書きのサンプルであることの明示にも使う
	AnalysisPages []AnalysisPage      `json:"analysisPages,omitempty"` // AnalysisPages は GetDocumentAnalysis のレスポンスをページングの順に並べたもの
	Detect        *DetectPage         `json:"detect,omitempty"`        // Detect は DetectDocumentText のレスポンス
}

// AnalysisPage は GetDocumentAnalysis のレスポンス 1 ページ分
type AnalysisPage struct {
	JobStatus        types.JobStatus         `json:"jobStatus"`
	Blocks           []types.Block           `json:"blocks,omitempty"`
	DocumentMetadata *types.DocumentMetadata `json:"documentMetadata,omitempty"`
	NextToken        string                  `json:"nextToken,omitempty"`
	StatusMessage    string                  `json:"statusMessage,omitempty"`
	Warnings         []types.Warning         `json:"warnings,omitempty"`
	ModelVersion     string                  `json:"modelVersion,omitempty"`
}

// DetectPage は DetectDocumentText のレスポンス
type DetectPage struct {
	Blocks           []types.Block           `json:"blocks,omitempty"`
	DocumentMetadata *types.DocumentMetadata `json:"documentMetadata,omitempty"`
	ModelVersion     string                  `json:"modelVersion,omitempty"`
}

// RecordingPath は論文 ID から記録ファイルのパスを組み立てる
func RecordingPath(dir, paperID string) string {
	return filepath.Join(dir, paperID+".json")
}

// LoadRecording は記録ファイルを読み込む
func LoadRecording(path string) (*Recording, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("textract: read recording %s: %w", path, err)
	}
	var rec Recording
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("textract: parse recording %s: %w", path, err)
	}
	if err := rec.Validate(); err != nil {
		return nil, fmt.Errorf("textract: recording %s: %w", path, err)
	}
	return &rec, nil
}

// WriteFile は記録をファイルへ書き出す
func (r *Recording) WriteFile(path string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("textract: encode recording: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("textract: create recording dir: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("textract: write recording %s: %w", path, err)
	}
	return nil
}

// Validate は再生に必要な整合性を検査する
//
// 手書きの記録が壊れている場合に再生時ではなく読み込み時に落とすためにある
func (r *Recording) Validate() error {
	if len(r.AnalysisPages) == 0 && r.Detect == nil {
		return errors.New("recording has neither analysis pages nor detect result")
	}
	for i, p := range r.AnalysisPages {
		if p.JobStatus == "" {
			return fmt.Errorf("analysis page %d has no job status", i)
		}
		last := i == len(r.AnalysisPages)-1
		if last && p.NextToken != "" {
			return fmt.Errorf("analysis page %d is the last page but has a next token", i)
		}
		if !last && p.NextToken == "" {
			return fmt.Errorf("analysis page %d is followed by another page but has no next token", i)
		}
	}
	if len(r.FeatureTypes) > 0 {
		if err := ValidateFeatureTypes(r.FeatureTypes); err != nil {
			return err
		}
	}
	return nil
}

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
	jobID := r.rec.JobID
	return &awstextract.StartDocumentAnalysisOutput{JobId: &jobID}, nil
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
	if out.JobStatus == types.JobStatusInProgress {
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
