package textract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// Recording は 1 本の論文に対する Textract の記録済みレスポンス
//
// 配置は testdata/textract/{論文 ID}.json
type Recording struct {
	PaperID       string                         `json:"paperId"`
	JobID         string                         `json:"jobId,omitempty"`
	FeatureTypes  []awstextracttypes.FeatureType `json:"featureTypes,omitempty"`
	RecordedAt    time.Time                      `json:"recordedAt"`
	Note          string                         `json:"note,omitempty"`          // Note は記録の出自を残すための備考で、手書きのサンプルであることの明示にも使う
	AnalysisPages []AnalysisPage                 `json:"analysisPages,omitempty"` // AnalysisPages は GetDocumentAnalysis のレスポンスをページングの順に並べたもの
	Detect        *DetectPage                    `json:"detect,omitempty"`        // Detect は DetectDocumentText のレスポンス
}

// AnalysisPage は GetDocumentAnalysis のレスポンス 1 ページ分
type AnalysisPage struct {
	JobStatus        awstextracttypes.JobStatus         `json:"jobStatus"`
	Blocks           []awstextracttypes.Block           `json:"blocks,omitempty"`
	DocumentMetadata *awstextracttypes.DocumentMetadata `json:"documentMetadata,omitempty"`
	NextToken        string                             `json:"nextToken,omitempty"`
	StatusMessage    string                             `json:"statusMessage,omitempty"`
	Warnings         []awstextracttypes.Warning         `json:"warnings,omitempty"`
	ModelVersion     string                             `json:"modelVersion,omitempty"`
}

// DetectPage は DetectDocumentText のレスポンス
type DetectPage struct {
	Blocks           []awstextracttypes.Block           `json:"blocks,omitempty"`
	DocumentMetadata *awstextracttypes.DocumentMetadata `json:"documentMetadata,omitempty"`
	ModelVersion     string                             `json:"modelVersion,omitempty"`
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
