package normalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// fixedClock は年の妥当性判定を決定的にする (上限は 2027 になる)
func fixedClock() time.Time {
	return time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
}

// readSynthetic は testdata の合成フィクスチャを読む
//
// 実レスポンス由来ではなく、両経路の出力の形と表現のゆれを模して手で書いたもの
func readSynthetic(t *testing.T, name string) domain.Document {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("フィクスチャの読み込みに失敗した: %v", err)
	}
	var doc domain.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("フィクスチャの解析に失敗した: %v", err)
	}
	return doc
}

// marshalMap は JSON をキーと値の集合へ落とす (null の有無やキーの省略を確かめるため)
func marshalMap(t *testing.T, doc domain.Document) map[string]any {
	t.Helper()

	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal に失敗した: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal に失敗した: %v", err)
	}
	return m
}

// hasWarning は正規化が足した警告 (接頭辞付き) に substr を含むものがあるかを返す
func hasWarning(doc domain.Document, substr string) bool {
	for _, w := range doc.Provenance.Warnings {
		if strings.HasPrefix(w, warningPrefix) && strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// 両経路の想定出力に同じ関数を掛けて domain.Document に収まり、経路差が消えていないことを確かめる
func TestNormalizeKeepsRouteDifferences(t *testing.T) {
	a := Normalize(readSynthetic(t, "synthetic-textract.json"), WithClock(fixedClock))
	b := Normalize(readSynthetic(t, "synthetic-bedrock.json"), WithClock(fixedClock))

	// 経路 A: bbox と Textract 由来の confidence が残ること
	if len(a.Figures[0].BBox) != 4 || len(a.Tables[0].BBox) != 4 {
		t.Errorf("経路 A の bbox が失われた: figures=%v tables=%v", a.Figures[0].BBox, a.Tables[0].BBox)
	}
	if a.Provenance.Confidence.Tables == nil || *a.Provenance.Confidence.Tables != 0.724 {
		t.Errorf("経路 A の confidence が失われた: %+v", a.Provenance.Confidence)
	}
	if a.Provenance.Confidence.Authors != nil {
		t.Errorf("経路 A で未算出の confidence が埋められた: %+v", a.Provenance.Confidence)
	}

	// 経路 B: bbox は空のまま、confidence は未設定のまま (埋めない・推定しない)
	if a.Provenance.Route != domain.RouteTextract || b.Provenance.Route != domain.RouteBedrock {
		t.Errorf("route が変わった: a=%q b=%q", a.Provenance.Route, b.Provenance.Route)
	}
	if b.Figures[0].BBox != nil || b.Tables[0].BBox != nil {
		t.Errorf("経路 B の bbox が埋められた: figures=%v tables=%v", b.Figures[0].BBox, b.Tables[0].BBox)
	}
	if !reflect.DeepEqual(b.Provenance.Confidence, domain.Confidence{}) {
		t.Errorf("経路 B の confidence が埋められた: %+v", b.Provenance.Confidence)
	}
	bm := marshalMap(t, b)
	if _, ok := bm["figures"].([]any)[0].(map[string]any)["bbox"]; ok {
		t.Error("経路 B の figures に bbox キーが出力された")
	}
	if b.Provenance.Cost.TextractPages != 0 || b.Provenance.Cost.TextractFeatures != nil {
		t.Errorf("経路 B に Textract のコストが入った: %+v", b.Provenance.Cost)
	}

	// 表の崩れ (列数の不揃い) は比較対象として残すこと
	if len(b.Tables[0].Rows[1]) != 1 {
		t.Errorf("経路 B の崩れた行が補われた: %q", b.Tables[0].Rows)
	}

	// 表現のゆれは揃うこと
	if a.Metadata.Title != "Sparse Attention Routing for Long-Context Language Models" {
		t.Errorf("経路 A の Title = %q", a.Metadata.Title)
	}
	if b.Metadata.Title != "疎な注意経路選択による長文脈言語モデルの高速化" {
		t.Errorf("経路 B の Title = %q", b.Metadata.Title)
	}
	if a.Sections[1].Level != 2 || b.Sections[2].Level != 2 {
		t.Errorf("番号付き見出しのレベルが揃っていない: a=%d b=%d", a.Sections[1].Level, b.Sections[2].Level)
	}
	if a.Sections[2].Level != 1 {
		t.Errorf("番号のない見出しのレベルが変わった: %d", a.Sections[2].Level)
	}
	if !reflect.DeepEqual(a.Sections[0].Pages, []int{1, 2}) || !reflect.DeepEqual(a.Sections[1].Pages, []int{4}) {
		t.Errorf("pages が昇順・重複なしになっていない: %v %v", a.Sections[0].Pages, a.Sections[1].Pages)
	}
	if a.References[0].Title != "Attention Is All You Need" {
		t.Errorf("経路 A の References[0].Title = %q", a.References[0].Title)
	}
	if want := "10.48550/arxiv.2309.17453"; *a.References[1].DOI != want || *b.References[1].DOI != want {
		t.Errorf("DOI が揃っていない: a=%q b=%q", *a.References[1].DOI, *b.References[1].DOI)
	}
	if !reflect.DeepEqual(a.Provenance.Cost.TextractFeatures, []string{"LAYOUT", "TABLES"}) {
		t.Errorf("textractFeatures が昇順になっていない: %v", a.Provenance.Cost.TextractFeatures)
	}
	if len(a.Provenance.Warnings) != 1 {
		t.Errorf("経路 A の重複した警告が除かれていない: %q", a.Provenance.Warnings)
	}

	// 経路 B の authors: null は [] に揃い、経路の警告は残り、正規化の警告が接頭辞付きで足されること
	if b.Metadata.Authors == nil || bm["metadata"].(map[string]any)["authors"] == nil {
		t.Errorf("経路 B の authors が null のまま: %v", bm["metadata"])
	}
	if b.Provenance.Warnings[0] != "ページの抽出結果が欠落したまま結合した: page=7" {
		t.Errorf("経路の警告が失われた: %q", b.Provenance.Warnings)
	}
	if !hasWarning(b, "sections[0] の見出しレベル 0 を 1 に丸めた") {
		t.Errorf("見出しなしの章のレベルの警告がない: %q", b.Provenance.Warnings)
	}
}

// 欠落したスライスが空配列になり、JSON に null が現れないこと (doi の null だけは未特定の意味で残る)
func TestNormalizeFillsMissingSlices(t *testing.T) {
	got := Normalize(domain.Document{
		Sections:   []domain.Section{{Level: 1, Heading: "1 Introduction"}},
		Tables:     []domain.Table{{ID: "table-1", Rows: [][]string{nil}}},
		References: []domain.Reference{{Raw: "r"}},
	}, WithClock(fixedClock))

	if got.Metadata.Authors == nil || got.Figures == nil || got.Sections[0].Pages == nil {
		t.Errorf("nil のスライスが残っている: %+v", got)
	}
	if got.Tables[0].Header == nil || got.Tables[0].Rows[0] == nil {
		t.Errorf("表の nil のスライスが残っている: %+v", got.Tables[0])
	}
	if got.References[0].DOI != nil {
		t.Errorf("DOI = %q, want nil (未特定は null のまま)", *got.References[0].DOI)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal に失敗した: %v", err)
	}
	s := strings.ReplaceAll(string(b), `"doi":null`, "")
	if strings.Contains(s, "null") {
		t.Errorf("JSON に null が含まれる: %s", b)
	}
	if !strings.Contains(string(b), `"doi":null`) {
		t.Errorf("doi の null が失われた: %s", b)
	}
	if got.SchemaVersion != domain.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", got.SchemaVersion, domain.SchemaVersion)
	}
}

func TestSortedPages(t *testing.T) {
	tests := map[string]struct {
		in   []int
		want []int
	}{
		"正常系_降順の場合_昇順になること":      {in: []int{4, 2, 3}, want: []int{2, 3, 4}},
		"正常系_重複がある場合_1 つに畳まれること": {in: []int{2, 1, 2, 1}, want: []int{1, 2}},
		"正常系_昇順で重複がない場合_変わらないこと": {in: []int{1, 2}, want: []int{1, 2}},
		"境界値_nil の場合_空配列になること":   {in: nil, want: []int{}},
		"境界値_空配列の場合_空配列のままであること": {in: []int{}, want: []int{}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := sortedPages(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sortedPages(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeProvenanceWarnings(t *testing.T) {
	got := Normalize(domain.Document{
		Provenance: domain.Provenance{
			Route:      domain.Route("unknown"),
			DurationMs: -1,
			Cost:       domain.Cost{TextractFeatures: []string{"TABLES", "LAYOUT", "TABLES"}},
			Warnings:   []string{"b", "a", "b"},
		},
	}, WithClock(fixedClock))

	if got.Provenance.Route != "unknown" || !got.Provenance.ExtractedAt.IsZero() || got.Provenance.DurationMs != -1 {
		t.Errorf("provenance の値が変わった (検証だけ行い値は変えないこと): %+v", got.Provenance)
	}
	if !reflect.DeepEqual(got.Provenance.Cost.TextractFeatures, []string{"LAYOUT", "TABLES"}) {
		t.Errorf("TextractFeatures = %v, want [LAYOUT TABLES]", got.Provenance.Cost.TextractFeatures)
	}
	if !reflect.DeepEqual(got.Provenance.Warnings[:2], []string{"b", "a"}) {
		t.Errorf("経路の警告の順序または重複除去が崩れた: %q", got.Provenance.Warnings)
	}
	for _, want := range []string{"jobId が空", "provenance.route が未知", "provenance.extractedAt が未設定", "provenance.durationMs が負"} {
		if !hasWarning(got, want) {
			t.Errorf("警告 %q がない: %q", want, got.Provenance.Warnings)
		}
	}
	for _, w := range got.Provenance.Warnings[2:] {
		if !strings.HasPrefix(w, warningPrefix) {
			t.Errorf("正規化の警告に接頭辞がない: %q", w)
		}
	}
}

// 揃った入力には警告を足さないこと (経路 A の想定出力に近い形)
func TestNormalizeCleanInputAddsNoWarning(t *testing.T) {
	got := Normalize(domain.Document{
		JobID:         "01JB8X7K2M9QRT4V6WZ3N5PDA0",
		SchemaVersion: domain.SchemaVersion,
		Provenance: domain.Provenance{
			Route:       domain.RouteTextract,
			ExtractedAt: fixedClock(),
		},
	}, WithClock(fixedClock))

	if got.Provenance.Warnings != nil {
		t.Errorf("Warnings = %q, want nil", got.Provenance.Warnings)
	}
}

// 表現のゆれを多く含む文書 (どのテストでも正規化が何かしら手を入れる)
func messyDocument() domain.Document {
	return domain.Document{
		JobID: "01JB8X7K2M9QRT4V6WZ3N5PDA0",
		Metadata: domain.Metadata{
			Title:    " Sparse\nAttention ",
			Authors:  nil,
			Keywords: []string{" sparse ", ""},
			Year:     20017,
		},
		Sections: []domain.Section{
			{Level: 0, Heading: "Introduction", Text: "para  \n", Pages: []int{2, 1, 2}},
			{Level: 1, Heading: "3.1  Setup", Pages: nil},
		},
		Figures: []domain.Figure{{ID: " figure-1 ", Page: 1, BBox: domain.BBox{0.1, 0.2, 0.3, 0.4}}},
		Tables:  []domain.Table{{ID: "table-1", Header: nil, Rows: [][]string{{" a ", "b"}, nil}}},
		References: []domain.Reference{
			{Raw: "x.\ny", Title: "T.", Authors: []string{"", "A"}, Year: 1, DOI: domain.Ptr("https://doi.org/10.1000/ABC")},
			{Raw: "z", Title: "Ends with ellipsis...", DOI: domain.Ptr("bogus")},
		},
		Provenance: domain.Provenance{
			Route:      domain.Route(""),
			Confidence: domain.Confidence{Title: domain.Ptr(0.9)},
			Cost:       domain.Cost{TextractFeatures: []string{"TABLES", "LAYOUT"}},
			Warnings:   []string{"w", "w"},
		},
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := messyDocument()
	before, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal に失敗した: %v", err)
	}

	got := Normalize(in, WithClock(fixedClock))

	// 出力側を書き換えても入力に波及しないこと (スライスとポインタを共有していない)
	got.Metadata.Keywords[0] = "changed"
	got.Sections[0].Pages[0] = 99
	got.Figures[0].BBox[0] = 0.99
	got.Tables[0].Rows[0][0] = "changed"
	got.References[0].Authors[0] = "changed"
	*got.References[0].DOI = "changed"
	*got.Provenance.Confidence.Title = 0.1
	got.Provenance.Cost.TextractFeatures[0] = "changed"
	got.Provenance.Warnings[0] = "changed"

	after, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal に失敗した: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("入力が変更された\nbefore: %s\n after: %s", before, after)
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	tests := map[string]struct {
		in domain.Document
	}{
		"正常系_ゆれを多く含む文書の場合_2 回掛けても結果が変わらないこと":  {in: messyDocument()},
		"正常系_経路 A の想定出力の場合_2 回掛けても結果が変わらないこと": {in: readSynthetic(t, "synthetic-textract.json")},
		"正常系_経路 B の想定出力の場合_2 回掛けても結果が変わらないこと": {in: readSynthetic(t, "synthetic-bedrock.json")},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			once := Normalize(tt.in, WithClock(fixedClock))
			twice := Normalize(once, WithClock(fixedClock))

			if !reflect.DeepEqual(once, twice) {
				t.Errorf("2 回掛けると結果が変わる\n once: %+v\ntwice: %+v", once, twice)
			}
			if len(once.Provenance.Warnings) == 0 {
				t.Fatalf("警告が 1 件も出ていない (テストの前提が崩れている)")
			}
		})
	}
}
