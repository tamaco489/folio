package textractroute_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
	// 本文はモデルの出力ではなく、記録が指した要素 (#1-#2) から組み立てる
	if doc.Sections[0].Text != "left one\n\nleft two" {
		t.Errorf("section text = %q, want 要素 #1-#2 の連結", doc.Sections[0].Text)
	}
	if !slices.Equal(doc.Sections[0].Pages, []int{1}) {
		t.Errorf("section pages = %v, want [1] (要素のページから求めること)", doc.Sections[0].Pages)
	}
	// from/to が -1 の節は見出しだけを残す
	if doc.Sections[2].Text != "" || len(doc.Sections[2].Pages) != 0 {
		t.Errorf("sections[2] = %+v, want 本文とページが空", doc.Sections[2])
	}

	if len(doc.References) != 1 || doc.References[0].DOI == nil || *doc.References[0].DOI != "10.1000/example.0001" {
		t.Errorf("references = %+v", doc.References)
	}
	// element を持たない応答は raw をそのまま使う
	if doc.References[0].Raw != "J. Doe. A Survey of Document Understanding. 2020." {
		t.Errorf("reference raw = %q", doc.References[0].Raw)
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
	if !strings.Contains(text.Text, "[#0 TITLE] Attention Is All You Need") {
		t.Errorf("user prompt に読み順のテキストが含まれない\n---\n%s", text.Text)
	}
}

// 節の本文と参考文献の原文を、モデルが指した要素番号から組み立てることを確かめる
//
// two-column.json の要素は #0 TITLE、#1-#4 TEXT (left one, left two, right one, right two)、#5 FIGURE、#6 TEXT (キャプション)
func TestExtractAssemblesTextFromElements(t *testing.T) {
	t.Parallel()

	fake := &fakeConverser{resp: &bedrock.Response{Text: `{
		"title": "t",
		"sections": [
			{"level": 1, "heading": "All", "from": 0, "to": 6},
			{"level": 2, "heading": "Reversed", "from": 4, "to": 3},
			{"level": 2, "heading": "Out of range", "from": 6, "to": 7}
		],
		"references": [
			{"element": 6, "title": "caption as reference"},
			{"element": 99, "title": "missing element"}
		]
	}`}}
	doc, err := textractroute.New(fake, "model-x").Extract(context.Background(), sampleInput(t))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(doc.Sections) != 3 {
		t.Fatalf("sections = %d, want 3", len(doc.Sections))
	}
	// 見出し (#0 TITLE) と図 (#5 FIGURE) は本文に含めない
	if want := "left one\n\nleft two\n\nright one\n\nright two\n\nFigure 1: The Transformer architecture."; doc.Sections[0].Text != want {
		t.Errorf("sections[0].Text = %q, want %q", doc.Sections[0].Text, want)
	}
	if !slices.Equal(doc.Sections[0].Pages, []int{1, 2}) {
		t.Errorf("sections[0].Pages = %v, want [1 2]", doc.Sections[0].Pages)
	}
	for i := 1; i <= 2; i++ {
		if doc.Sections[i].Text != "" || doc.Sections[i].Heading == "" {
			t.Errorf("sections[%d] = %+v, want 見出しだけを残し本文は空", i, doc.Sections[i])
		}
	}

	if len(doc.References) != 2 {
		t.Fatalf("references = %d, want 2", len(doc.References))
	}
	if doc.References[0].Raw != "Figure 1: The Transformer architecture." {
		t.Errorf("references[0].Raw = %q, want 要素 #6 の文字列", doc.References[0].Raw)
	}
	if doc.References[1].Raw != "" {
		t.Errorf("references[1].Raw = %q, want 空", doc.References[1].Raw)
	}

	for _, want := range []string{"sections[1]", "sections[2]", "references[1]"} {
		if !slices.ContainsFunc(doc.Provenance.Warnings, func(w string) bool { return strings.HasPrefix(w, want) }) {
			t.Errorf("warnings = %q, want %s の警告", doc.Provenance.Warnings, want)
		}
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
		"異常系_出力が上限で打ち切られた場合_JSON 復号の失敗ではなく ErrOutputTruncated になること": {
			converser: &fakeConverser{resp: &bedrock.Response{
				Text:       `{"title":"t","sections":[{"heading":"1 Intro`,
				StopReason: bedrock.StopReasonMaxTokens,
				Usage:      bedrock.Usage{OutputTokens: 8192},
			}},
			modelID: "model-x",
			want:    bedrock.ErrOutputTruncated,
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
