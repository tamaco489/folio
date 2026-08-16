package textractroute_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/textractroute"
)

// bedrockDir は記録済み Bedrock レスポンスの置き場所 (backend/testdata)
const bedrockDir = "../../../../testdata/bedrock"

const recordedModelID = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"

// fakeConverser は記録に無い応答を返すためのフェイク
type fakeConverser struct {
	resp *bedrock.Response
	err  error
	req  bedrock.Request
}

var _ bedrock.Converser = (*fakeConverser)(nil)

func (f *fakeConverser) Converse(_ context.Context, req bedrock.Request) (*bedrock.Response, error) {
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func sampleInput(t *testing.T) textractroute.Input {
	t.Helper()

	return textractroute.Input{
		JobID:   "job-1",
		PaperID: "2301.07041",
		Source: domain.Source{
			Bucket:     "folio-dev",
			Key:        "uploads/job-1/original.pdf",
			Language:   domain.LanguageEnglish,
			PageCount:  2,
			UploadedAt: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		},
		Analysis:     analyze(t, filepath.Join(fixtureDir, "two-column.json")),
		FeatureTypes: []awstextracttypes.FeatureType{awstextracttypes.FeatureTypeLayout, awstextracttypes.FeatureTypeTables},
	}
}

// 記録済みレスポンスの再生だけで domain.Document まで到達できることを確かめる
func TestExtractReplaysRecordedResponse(t *testing.T) {
	t.Parallel()

	doc, err := textractroute.New(bedrock.NewReplayer(bedrockDir), recordedModelID).
		Extract(context.Background(), sampleInput(t))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if doc.JobID != "job-1" || doc.SchemaVersion != domain.SchemaVersion {
		t.Errorf("document = %+v", doc)
	}
	if doc.Source.Key != "uploads/job-1/original.pdf" {
		t.Errorf("source = %+v (前段の値を素通しすること)", doc.Source)
	}

	if doc.Metadata.Title != "Learning to Structure Scientific Documents" {
		t.Errorf("title = %q", doc.Metadata.Title)
	}
	if len(doc.Metadata.Authors) != 2 || doc.Metadata.Authors[0].Affiliation != "Example University" {
		t.Errorf("authors = %+v", doc.Metadata.Authors)
	}
	if !strings.HasPrefix(doc.Metadata.Abstract, "We present a pipeline") {
		t.Errorf("abstract = %q", doc.Metadata.Abstract)
	}

	if len(doc.Sections) != 3 {
		t.Fatalf("sections = %d, want 3", len(doc.Sections))
	}
	if doc.Sections[0].Heading != "1 Introduction" {
		t.Errorf("section heading = %q", doc.Sections[0].Heading)
	}
	// 記録は単数の page を返すため、domain の pages へ畳めていることを確かめる
	if len(doc.Sections[0].Pages) != 1 || doc.Sections[0].Pages[0] != 1 {
		t.Errorf("section pages = %v, want [1]", doc.Sections[0].Pages)
	}

	if len(doc.References) != 1 || doc.References[0].DOI == nil || *doc.References[0].DOI != "10.1000/example.0001" {
		t.Errorf("references = %+v", doc.References)
	}

	// 図と表はモデルではなく Read の結果を使う
	if len(doc.Figures) != 1 || doc.Figures[0].ID != "figure-1" {
		t.Errorf("figures = %+v", doc.Figures)
	}
	if doc.Tables == nil {
		t.Error("tables = nil, want 空配列")
	}

	p := doc.Provenance
	if p.Route != domain.RouteTextract {
		t.Errorf("route = %q, want textract", p.Route)
	}
	if p.Cost.BedrockModel != recordedModelID {
		t.Errorf("bedrockModel = %q", p.Cost.BedrockModel)
	}
	if p.Cost.BedrockInputTokens != 4821 || p.Cost.BedrockOutputTokens != 318 {
		t.Errorf("cost = %+v", p.Cost)
	}
	if p.Cost.TextractPages != 2 {
		t.Errorf("textractPages = %d, want 2", p.Cost.TextractPages)
	}
	if got := strings.Join(p.Cost.TextractFeatures, ","); got != "LAYOUT,TABLES" {
		t.Errorf("textractFeatures = %q", got)
	}
	if p.Confidence.Title == nil {
		t.Error("confidence.title = nil, want Textract の確信度")
	}
	if len(p.Warnings) == 0 {
		t.Error("warnings = 空, want Read が残した警告")
	}
	// 経過時刻は normalize と finalizer が埋めるため、この層では触らない
	if !p.ExtractedAt.IsZero() || p.DurationMs != 0 {
		t.Errorf("extractedAt = %v, durationMs = %d, want いずれもゼロ値", p.ExtractedAt, p.DurationMs)
	}
}

// 組み立てたリクエストが記録・再生の規約と読み順のテキストを満たすことを確かめる
func TestExtractBuildsRequest(t *testing.T) {
	t.Parallel()

	fake := &fakeConverser{resp: &bedrock.Response{Text: `{"title":"t"}`}}
	if _, err := textractroute.New(fake, "model-x").Extract(context.Background(), sampleInput(t)); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	req := fake.req
	if req.ModelID != "model-x" {
		t.Errorf("modelId = %q", req.ModelID)
	}
	if req.RecordKey != bedrock.RecordKey("2301.07041", bedrock.RouteTextract) {
		t.Errorf("recordKey = %q", req.RecordKey)
	}
	if req.MaxTokens == nil || *req.MaxTokens <= 0 {
		t.Errorf("maxTokens = %v, want 正の上限", req.MaxTokens)
	}
	// 経路間の差分を安定させるため温度は 0 に固定する
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", req.Temperature)
	}
	if !strings.Contains(req.System, `"sections"`) {
		t.Error("system prompt に出力スキーマが含まれない")
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 1 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	text, ok := req.Messages[0].Content[0].(bedrock.TextPart)
	if !ok {
		t.Fatalf("content = %T, want TextPart", req.Messages[0].Content[0])
	}
	if !strings.Contains(text.Text, "[TITLE] Attention Is All You Need") {
		t.Errorf("user prompt に読み順のテキストが含まれない\n---\n%s", text.Text)
	}
}

func TestExtractErrors(t *testing.T) {
	t.Parallel()

	converseErr := errors.New("throttled")

	tests := map[string]struct {
		converser bedrock.Converser
		modelID   string
		want      error
	}{
		"異常系_JSON として解釈できない応答の場合_リトライせずエラーになること": {
			converser: &fakeConverser{resp: &bedrock.Response{Text: "I could not read the paper."}},
			modelID:   "model-x",
			want:      bedrock.ErrInvalidJSON,
		},
		"異常系_Converse が失敗した場合_そのエラーを返すこと": {
			converser: &fakeConverser{err: converseErr},
			modelID:   "model-x",
			want:      converseErr,
		},
		"異常系_モデル ID が無い場合_課金を伴う呼び出しをせずエラーになること": {
			converser: &fakeConverser{},
			modelID:   "",
			want:      bedrock.ErrModelIDRequired,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := textractroute.New(tt.converser, tt.modelID).Extract(context.Background(), sampleInput(t))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}

	// モデル ID が無い場合は Converse まで到達してはならない
	fake := &fakeConverser{}
	if _, err := textractroute.New(fake, "").Extract(context.Background(), sampleInput(t)); err == nil {
		t.Fatal("err = nil")
	}
	if fake.req.RecordKey != "" {
		t.Error("モデル ID が無いまま Converse を呼んでいる")
	}
}
