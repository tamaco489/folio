package bedrockroute

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/domain"
)

// testdataDir は記録済みレスポンスの配置先 (規約上 backend/testdata/bedrock)
func testdataDir() string {
	return filepath.Join("..", "..", "..", "..", "testdata", "bedrock")
}

const (
	samplePaperID = "2301.07041"
	sampleModelID = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
)

// samplePNG は画像の中身を検証しないテストで用いるダミーのバイト列
var samplePNG = []byte{0x89, 'P', 'N', 'G'}

// fakeConverser は bedrock.Converser のフェイク (実 API は一切呼ばない)
type fakeConverser struct {
	reqs       []bedrock.Request
	input      string // input は tool use の入力 (JSON)
	text       string // text は tool use を経ない自由文の応答
	stopReason string // stopReason は空なら tool_use
	err        error
}

var _ bedrock.Converser = (*fakeConverser)(nil)

func (f *fakeConverser) Converse(_ context.Context, req bedrock.Request) (*bedrock.Response, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	stop := f.stopReason
	if stop == "" {
		stop = "tool_use"
	}
	return &bedrock.Response{
		Text:       f.text,
		ToolInput:  json.RawMessage(f.input),
		StopReason: stop,
		Usage:      bedrock.Usage{InputTokens: 1200, OutputTokens: 340, TotalTokens: 1540},
	}, nil
}

func (f *fakeConverser) calls() int { return len(f.reqs) }

// 記録済みレスポンスを再生し、実 API を呼ばずにページ結果まで取り出せることを確かめる
//
// 2301.07041-bedrock.json は 1 ページ分 ("page":1 と表紙の書誌情報のみ) を返す記録であり、そのまま 1 ページ目の入力として扱える
func TestExtractPageReplaysRecording(t *testing.T) {
	e := New(bedrock.NewReplayer(testdataDir()), sampleModelID)

	got, err := e.ExtractPage(context.Background(), PageInput{
		Page:      1,
		Image:     samplePNG,
		Language:  domain.LanguageEnglish,
		RecordKey: bedrock.RecordKey(samplePaperID, bedrock.RouteBedrock),
	})
	if err != nil {
		t.Fatalf("ExtractPage: %v", err)
	}

	if got.Page != 1 {
		t.Errorf("Page = %d, want 1", got.Page)
	}
	if got.Title != "Learning to Structure Scientific Documents" {
		t.Errorf("Title = %q", got.Title)
	}
	if len(got.Authors) != 2 || got.Authors[0].Affiliation != "Example University" {
		t.Errorf("Authors = %+v", got.Authors)
	}
	if got.Abstract == "" {
		t.Error("Abstract が空になっている")
	}
	if len(got.Sections) != 1 || got.Sections[0].Heading != "1 Introduction" {
		t.Errorf("Sections = %+v", got.Sections)
	}
	if len(got.Figures) != 1 || got.Figures[0].Label != "Figure 1" {
		t.Errorf("Figures = %+v", got.Figures)
	}

	// Usage は結合時に provenance.cost へ合算するため、記録から取り出せている必要がある
	if got.Usage.InputTokens != 2317 || got.Usage.OutputTokens != 264 {
		t.Errorf("Usage = %+v, want input=2317 output=264", got.Usage)
	}
	if got.ModelID != sampleModelID {
		t.Errorf("ModelID = %q, want %q", got.ModelID, sampleModelID)
	}
}

// 引用符を含む本文が tool use の記録を再生しても壊れずに復元できることを確かめる
//
// 2608.19529-bedrock.json は Issue #111 で PageDecodeError になったページを模した合成の記録
func TestExtractPageReplaysQuotedText(t *testing.T) {
	e := New(bedrock.NewReplayer(testdataDir()), sampleModelID)

	got, err := e.ExtractPage(context.Background(), PageInput{
		Page:      11,
		Image:     samplePNG,
		RecordKey: bedrock.RecordKey("2608.19529", bedrock.RouteBedrock),
	})
	if err != nil {
		t.Fatalf("ExtractPage: %v", err)
	}

	if len(got.Sections) != 2 {
		t.Fatalf("Sections = %+v, want 2 件", got.Sections)
	}
	if want := `The word "Beauty" was shown`; !strings.Contains(got.Sections[0].Text, want) {
		t.Errorf("Sections[0].Text = %q, want %q を含むこと", got.Sections[0].Text, want)
	}
	if want := `We "prove" nothing`; !strings.Contains(got.Sections[1].Text, want) {
		t.Errorf("Sections[1].Text = %q, want %q を含むこと", got.Sections[1].Text, want)
	}
	if len(got.References) != 1 || got.References[0].Raw != `A. Author. "Quoted Title". Journal, 2024.` {
		t.Errorf("References = %+v", got.References)
	}
	if got.ContinuesPreviousSection == nil || !*got.ContinuesPreviousSection {
		t.Errorf("ContinuesPreviousSection = %v, want true", got.ContinuesPreviousSection)
	}
}

// 経路 B の Request がページ画像と指示テキストの組で組み立てられ、tool use でスキーマを渡すことを確かめる
func TestExtractPageBuildsRequest(t *testing.T) {
	conv := &fakeConverser{input: `{"page":7}`}
	e := New(conv, sampleModelID)

	if _, err := e.ExtractPage(context.Background(), PageInput{
		Page:     7,
		Image:    samplePNG,
		Language: domain.LanguageJapanese,
	}); err != nil {
		t.Fatalf("ExtractPage: %v", err)
	}

	req := conv.reqs[0]
	if req.ModelID != sampleModelID {
		t.Errorf("ModelID = %q, want %q", req.ModelID, sampleModelID)
	}
	if req.System == "" {
		t.Error("System プロンプトが空になっている")
	}
	if req.MaxTokens == nil || *req.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %v, want %d", req.MaxTokens, maxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Errorf("Temperature = %v, want 0", req.Temperature)
	}
	if req.RecordKey != "" {
		t.Errorf("RecordKey = %q, want 空 (本番の呼び出しでは記録しない)", req.RecordKey)
	}
	if req.Tool == nil || req.Tool.Name != pageTool.Name {
		t.Fatalf("Tool = %+v, want %s", req.Tool, pageTool.Name)
	}
	if !strings.Contains(req.System, req.Tool.Name) {
		t.Errorf("System プロンプトが tool の名前 %q に触れていない", req.Tool.Name)
	}
	if strings.Contains(req.System, `"title": string`) {
		t.Error("System プロンプトにスキーマの断片が残っている (形は Tool.Schema で渡す)")
	}

	if len(req.Messages) != 1 {
		t.Fatalf("Messages の件数 = %d, want 1 (ページ画像は 1 枚ずつ渡す)", len(req.Messages))
	}
	content := req.Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("content block の件数 = %d, want 2", len(content))
	}
	img, ok := content[0].(bedrock.ImagePart)
	if !ok {
		t.Fatalf("content[0] = %T, want ImagePart", content[0])
	}
	if img.Format != bedrock.ImageFormatPNG || string(img.Bytes) != string(samplePNG) {
		t.Errorf("ImagePart = %+v", img)
	}
	text, ok := content[1].(bedrock.TextPart)
	if !ok {
		t.Fatalf("content[1] = %T, want TextPart", content[1])
	}
	if !strings.Contains(text.Text, "page 7") {
		t.Errorf("指示テキストにページ番号が含まれていない: %q", text.Text)
	}
	if !strings.Contains(text.Text, "ja") {
		t.Errorf("指示テキストに言語ヒントが含まれていない: %q", text.Text)
	}
}

// スキーマのキーが PageResult の json タグと一致することを確かめる (食い違うと tool の入力が黙って捨てられる)
func TestPageToolSchemaMatchesPageResult(t *testing.T) {
	props, ok := pageTool.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T", pageTool.Schema["properties"])
	}

	want := map[string]any{
		"title": "t", "abstract": "a", "keywords": []string{"k"},
		"authors":                    []domain.Author{{Name: "n", Affiliation: "f", Email: "e"}},
		"sections":                   []PageSection{{Level: 1, Heading: "h", Text: "x"}},
		"figures":                    []PageFigure{{Label: "l", Caption: "c"}},
		"tables":                     []PageTable{{Label: "l", Caption: "c", Header: [][]string{{"h"}}, Rows: [][]string{{"r"}}}},
		"references":                 []PageReference{{Raw: "r", Title: "t", Authors: []string{"a"}, Year: 1, Venue: "v", DOI: "d"}},
		"continuesPreviousSection":   true,
		"continuesPreviousReference": true,
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for key := range got {
		if _, ok := props[key]; !ok {
			t.Errorf("スキーマに %q が無い", key)
		}
	}
	for key := range props {
		if _, ok := got[key]; !ok {
			t.Errorf("スキーマの %q が PageResult に無い", key)
		}
	}
	assertObjectKeys(t, props, "authors", "name", "affiliation", "email")
	assertObjectKeys(t, props, "sections", "level", "heading", "text")
	assertObjectKeys(t, props, "figures", "label", "caption")
	assertObjectKeys(t, props, "tables", "label", "caption", "header", "rows")
	assertObjectKeys(t, props, "references", "raw", "title", "authors", "year", "venue", "doi")
}

// assertObjectKeys は配列プロパティの要素 object が持つキーの集合を確かめる
func assertObjectKeys(t *testing.T, props map[string]any, name string, keys ...string) {
	t.Helper()
	items, _ := props[name].(map[string]any)["items"].(map[string]any)
	got, _ := items["properties"].(map[string]any)
	if len(got) != len(keys) {
		t.Errorf("%s の要素のキー = %v, want %v", name, got, keys)
	}
	for _, k := range keys {
		if _, ok := got[k]; !ok {
			t.Errorf("%s の要素に %q が無い", name, k)
		}
	}
}

// 言語ヒントを省略した場合に言語の指定が付かないことを確かめる
func TestPagePromptWithoutLanguage(t *testing.T) {
	got := pagePrompt(3, "")
	if !strings.Contains(got, "page 3") {
		t.Errorf("pagePrompt = %q", got)
	}
	if strings.Contains(got, "ISO 639-1") {
		t.Errorf("言語ヒントが無いのに言語の指定が入っている: %q", got)
	}
}

// パースに失敗した応答をリトライせずエラーとして返すことを確かめる
func TestExtractPageDecodeFailure(t *testing.T) {
	tests := map[string]struct {
		input      string
		text       string
		stopReason string
		want       error
	}{
		"異常系_tool use も JSON も含まない応答の場合_ErrPageDecode が返ること":              {text: "この画像からは読み取れませんでした", stopReason: "end_turn", want: bedrock.ErrInvalidJSON},
		"異常系_tool の入力が壊れている応答の場合_ErrPageDecode が返ること":                     {input: `{"page": 1, "title":}`, want: bedrock.ErrInvalidJSON},
		"異常系_出力が上限で打ち切られた応答の場合_ErrPageDecode と ErrOutputTruncated を満たすこと": {text: `{"page": 1, "sections": [{"heading": "1 Intro`, stopReason: bedrock.StopReasonMaxTokens, want: bedrock.ErrOutputTruncated},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			conv := &fakeConverser{input: tt.input, text: tt.text, stopReason: tt.stopReason}
			e := New(conv, sampleModelID)

			_, err := e.ExtractPage(context.Background(), PageInput{Page: 1, Image: samplePNG})
			if !errors.Is(err, ErrPageDecode) {
				t.Fatalf("err = %v, want ErrPageDecode", err)
			}
			// 呼び出し側が再送を判断できるよう、応答の由来も辿れる必要がある
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v も満たすこと", err, tt.want)
			}
			if conv.calls() != 1 {
				t.Errorf("Converse の呼び出し回数 = %d, want 1 (パース失敗をこの層で再送しない)", conv.calls())
			}
		})
	}
}

// 入力の検証で弾いた場合に課金を伴う呼び出しが起きないことを確かめる
func TestExtractPageValidation(t *testing.T) {
	tests := map[string]struct {
		in   PageInput
		want error
	}{
		"異常系_ページ画像が空の場合_ErrEmptyImage が返ること":    {in: PageInput{Page: 1}, want: ErrEmptyImage},
		"境界値_ページ番号が 0 の場合_ErrInvalidPage が返ること": {in: PageInput{Page: 0, Image: samplePNG}, want: ErrInvalidPage},
		"境界値_ページ番号が負の場合_ErrInvalidPage が返ること":   {in: PageInput{Page: -1, Image: samplePNG}, want: ErrInvalidPage},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			conv := &fakeConverser{input: "{}"}
			e := New(conv, sampleModelID)

			if _, err := e.ExtractPage(context.Background(), tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if conv.calls() != 0 {
				t.Errorf("Converse の呼び出し回数 = %d, want 0 (課金を伴う呼び出しをしてはならない)", conv.calls())
			}
		})
	}
}

// Converse のエラーがそのまま辿れることを確かめる
func TestExtractPagePropagatesConverseError(t *testing.T) {
	want := errors.New("throttled")
	e := New(&fakeConverser{err: want}, sampleModelID)

	if _, err := e.ExtractPage(context.Background(), PageInput{Page: 2, Image: samplePNG}); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// ページ結果は Map の出力として S3 を経由するため、JSON で往復しても意味が変わらないことを確かめる
func TestPageResultRoundTrip(t *testing.T) {
	want := PageResult{
		Page:                       4,
		Sections:                   []PageSection{{Text: "前ページからの続き"}, {Level: 2, Heading: "2.1 経路選択関数", Text: "関連度スコアを計算する"}},
		Figures:                    []PageFigure{{Label: "図 1", Caption: "全体像"}},
		Tables:                     []PageTable{{Label: "表 1", Caption: "比較", Header: [][]string{{"手法", "精度"}}, Rows: [][]string{{"提案", "0.9"}}}},
		References:                 []PageReference{{Raw: "Vaswani, A. ほか. Attention Is All You Need. NeurIPS, 2017.", Year: 2017}},
		ContinuesPreviousSection:   new(false),
		ContinuesPreviousReference: new(true),
		ModelID:                    sampleModelID,
		Usage:                      bedrock.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got PageResult
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// フラグは欠落と false を区別する必要があるため、値だけでなく nil でないことも確かめる
	if got.ContinuesPreviousSection == nil || *got.ContinuesPreviousSection {
		t.Errorf("ContinuesPreviousSection = %v, want false (nil ではない)", got.ContinuesPreviousSection)
	}
	if got.ContinuesPreviousReference == nil || !*got.ContinuesPreviousReference {
		t.Errorf("ContinuesPreviousReference = %v, want true", got.ContinuesPreviousReference)
	}
	if got.Page != want.Page || got.Usage != want.Usage || got.ModelID != want.ModelID {
		t.Errorf("往復で値が変わった: %+v", got)
	}
	if len(got.Sections) != 2 || got.Sections[1].Heading != "2.1 経路選択関数" {
		t.Errorf("Sections = %+v", got.Sections)
	}
	if len(got.Tables) != 1 || got.Tables[0].Header[0][1] != "精度" {
		t.Errorf("Tables = %+v", got.Tables)
	}

	// フラグが省略された記録を読んでも欠落として区別できること
	var absent PageResult
	if err := json.Unmarshal([]byte(`{"page":1}`), &absent); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if absent.ContinuesPreviousSection != nil || absent.ContinuesPreviousReference != nil {
		t.Errorf("フラグが省略された場合に nil にならない: %+v", absent)
	}
}
