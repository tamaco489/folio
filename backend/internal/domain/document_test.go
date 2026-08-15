package domain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tamaco489/folio/backend/internal/domain"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("フィクスチャの読み込みに失敗した: %v", err)
	}
	return b
}

// asAny は JSON をキーと値の集合へ落とす
//
// バイト列の比較ではインデントやキー順の差で落ちるため、意味的な同値で比較する
func asAny(t *testing.T, b []byte) any {
	t.Helper()

	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("JSON の解析に失敗した: %v", err)
	}
	return v
}

func TestDocumentRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{name: "経路 A の抽出結果", fixture: "result-textract.json"},
		{name: "経路 B の抽出結果 (欠落しうるフィールドを含む)", fixture: "result-bedrock.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := readFixture(t, tt.fixture)

			var doc domain.Document
			if err := json.Unmarshal(original, &doc); err != nil {
				t.Fatalf("Unmarshal に失敗した: %v", err)
			}

			encoded, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("Marshal に失敗した: %v", err)
			}

			if got, want := asAny(t, encoded), asAny(t, original); !reflect.DeepEqual(got, want) {
				t.Errorf("キーまたは値がサンプルと一致しない\n got: %s\nwant: %s", encoded, original)
			}

			var reparsed domain.Document
			if err := json.Unmarshal(encoded, &reparsed); err != nil {
				t.Fatalf("再 Unmarshal に失敗した: %v", err)
			}
			if !reflect.DeepEqual(reparsed, doc) {
				t.Errorf("往復で構造体が変化した\n got: %+v\nwant: %+v", reparsed, doc)
			}
		})
	}
}

func TestUnmarshalTextractSample(t *testing.T) {
	var doc domain.Document
	if err := json.Unmarshal(readFixture(t, "result-textract.json"), &doc); err != nil {
		t.Fatalf("Unmarshal に失敗した: %v", err)
	}

	if doc.JobID != "01JB8X7K2M9QRT4V6WZ3N5PDA0" {
		t.Errorf("JobID = %q", doc.JobID)
	}
	if doc.SchemaVersion != domain.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", doc.SchemaVersion, domain.SchemaVersion)
	}
	if doc.Source.Language != domain.LanguageEnglish {
		t.Errorf("Source.Language = %q", doc.Source.Language)
	}
	if !doc.Source.HasTextLayer {
		t.Error("Source.HasTextLayer = false, want true")
	}
	if got := doc.Source.UploadedAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-08-15T02:14:33Z" {
		t.Errorf("Source.UploadedAt = %q", got)
	}
	if len(doc.Metadata.Authors) != 2 {
		t.Fatalf("Metadata.Authors の件数 = %d, want 2", len(doc.Metadata.Authors))
	}
	if doc.Metadata.Authors[0].Email != "tanaka@example.ac.jp" {
		t.Errorf("Metadata.Authors[0].Email = %q", doc.Metadata.Authors[0].Email)
	}
	if doc.Sections[2].Level != 2 || !reflect.DeepEqual(doc.Sections[2].Pages, []int{4}) {
		t.Errorf("Sections[2] = %+v", doc.Sections[2])
	}

	if len(doc.Figures[0].BBox) != 4 {
		t.Fatalf("Figures[0].BBox の要素数 = %d, want 4", len(doc.Figures[0].BBox))
	}
	if got := doc.Figures[0].BBox; got[0] != 0.118 || got[3] != 0.371 {
		t.Errorf("Figures[0].BBox = %v", got)
	}

	table := doc.Tables[0]
	if len(table.Header) != 2 {
		t.Fatalf("Tables[0].Header の段数 = %d, want 2", len(table.Header))
	}
	if table.Header[1][0] != "" || table.Header[1][1] != "1K" {
		t.Errorf("Tables[0].Header[1] = %v", table.Header[1])
	}
	if len(table.Rows) != 5 {
		t.Errorf("Tables[0].Rows の件数 = %d, want 5", len(table.Rows))
	}

	if doc.References[0].DOI != nil {
		t.Errorf("References[0].DOI = %v, want nil", *doc.References[0].DOI)
	}
	if doc.References[0].Year != 2017 {
		t.Errorf("References[0].Year = %d", doc.References[0].Year)
	}

	if doc.Provenance.Route != domain.RouteTextract {
		t.Errorf("Provenance.Route = %q", doc.Provenance.Route)
	}
	if doc.Provenance.Confidence.Tables == nil || *doc.Provenance.Confidence.Tables != 0.724 {
		t.Errorf("Provenance.Confidence.Tables = %v", doc.Provenance.Confidence.Tables)
	}
	if doc.Provenance.Cost.TextractPages != 11 {
		t.Errorf("Provenance.Cost.TextractPages = %d", doc.Provenance.Cost.TextractPages)
	}
	if doc.Provenance.DurationMs != 74210 {
		t.Errorf("Provenance.DurationMs = %d", doc.Provenance.DurationMs)
	}
	if len(doc.Provenance.Warnings) != 2 {
		t.Errorf("Provenance.Warnings の件数 = %d, want 2", len(doc.Provenance.Warnings))
	}
}

func TestUnmarshalBedrockSample(t *testing.T) {
	var doc domain.Document
	if err := json.Unmarshal(readFixture(t, "result-bedrock.json"), &doc); err != nil {
		t.Fatalf("Unmarshal に失敗した: %v", err)
	}

	if doc.Source.Language != domain.LanguageJapanese {
		t.Errorf("Source.Language = %q", doc.Source.Language)
	}
	if doc.Provenance.Route != domain.RouteBedrock {
		t.Errorf("Provenance.Route = %q", doc.Provenance.Route)
	}
	if doc.Metadata.Keywords != nil || doc.Metadata.Venue != "" || doc.Metadata.Year != 0 {
		t.Errorf("欠落した任意フィールドがゼロ値になっていない: %+v", doc.Metadata)
	}
	if doc.Figures[0].BBox != nil {
		t.Errorf("Figures[0].BBox = %v, want nil", doc.Figures[0].BBox)
	}
	if doc.Tables == nil || len(doc.Tables) != 0 {
		t.Errorf("Tables = %v, want 空配列", doc.Tables)
	}
	if doc.Provenance.Cost.TextractPages != 0 || doc.Provenance.Cost.TextractFeatures != nil {
		t.Errorf("経路 B に Textract のコストが入っている: %+v", doc.Provenance.Cost)
	}

	// 未算出 (nil) と 0 を区別できることを確認する
	if doc.Provenance.Confidence.Tables != nil {
		t.Errorf("Confidence.Tables = %v, want nil", doc.Provenance.Confidence.Tables)
	}
	if doc.Provenance.Confidence.Figures == nil || *doc.Provenance.Confidence.Figures != 0 {
		t.Errorf("Confidence.Figures = %v, want 0", doc.Provenance.Confidence.Figures)
	}
}

func TestMarshalKeyPresence(t *testing.T) {
	encoded, err := json.Marshal(domain.Document{})
	if err != nil {
		t.Fatalf("Marshal に失敗した: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal に失敗した: %v", err)
	}

	required := []string{
		"jobId", "schemaVersion", "source", "metadata",
		"sections", "figures", "tables", "references", "provenance",
	}
	for _, key := range required {
		if _, ok := got[key]; !ok {
			t.Errorf("トップレベルに %q が無い", key)
		}
	}
	if len(got) != len(required) {
		t.Errorf("トップレベルのキー数 = %d, want %d", len(got), len(required))
	}
}

func TestMarshalOmitsOptionalFields(t *testing.T) {
	doc := domain.Document{
		JobID:         "01JB9Q4T7C2E8HXM5KRV0YWN3B",
		SchemaVersion: domain.SchemaVersion,
		Metadata:      domain.Metadata{Title: "タイトル"},
		Figures:       []domain.Figure{{ID: "図 1", Page: 2}},
		References:    []domain.Reference{{Raw: "参照文献"}},
		Provenance: domain.Provenance{
			Route:      domain.RouteBedrock,
			Confidence: domain.Confidence{Title: domain.Ptr(0.0)},
		},
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal に失敗した: %v", err)
	}

	var got struct {
		Metadata   map[string]json.RawMessage   `json:"metadata"`
		Figures    []map[string]json.RawMessage `json:"figures"`
		References []map[string]json.RawMessage `json:"references"`
		Provenance struct {
			Confidence map[string]json.RawMessage `json:"confidence"`
			Cost       map[string]json.RawMessage `json:"cost"`
			Warnings   json.RawMessage            `json:"warnings"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal に失敗した: %v", err)
	}

	for _, key := range []string{"keywords", "venue", "year", "primaryCategory"} {
		if _, ok := got.Metadata[key]; ok {
			t.Errorf("metadata に %q が残っている", key)
		}
	}
	for _, key := range []string{"title", "authors", "abstract"} {
		if _, ok := got.Metadata[key]; !ok {
			t.Errorf("metadata に %q が無い", key)
		}
	}
	if _, ok := got.Figures[0]["bbox"]; ok {
		t.Error("figures に bbox が残っている")
	}
	if _, ok := got.References[0]["doi"]; !ok {
		t.Error("references に doi が無い")
	}
	if _, ok := got.References[0]["year"]; ok {
		t.Error("references に year が残っている")
	}
	if got.Provenance.Warnings != nil {
		t.Errorf("provenance に warnings が残っている: %s", got.Provenance.Warnings)
	}
	if len(got.Provenance.Confidence) != 1 {
		t.Errorf("confidence のキー数 = %d, want 1", len(got.Provenance.Confidence))
	}
	if string(got.Provenance.Confidence["title"]) != "0" {
		t.Errorf("confidence.title = %s, want 0", got.Provenance.Confidence["title"])
	}
	if _, ok := got.Provenance.Cost["textractPages"]; ok {
		t.Error("cost に textractPages が残っている")
	}
	if _, ok := got.Provenance.Cost["bedrockInputTokens"]; !ok {
		t.Error("cost に bedrockInputTokens が無い")
	}
}
