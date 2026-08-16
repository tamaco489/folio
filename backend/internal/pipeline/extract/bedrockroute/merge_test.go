package bedrockroute

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/domain"
)

// 日本語のページ結果を順不同で渡し、章の結合とページ番号の整列がまとめて成り立つことを確かめる
//
// 日本語の文書は経路 B しか通らないため、結合の基準となるこのテストを日本語で書く
func TestMergeJoinsSectionsAcrossPages(t *testing.T) {
	doc := Merge(MergeInput{
		JobID:  "01JB9Q4T7C2E8HXM5KRV0YWN3B",
		Source: domain.Source{Language: domain.LanguageJapanese, PageCount: 4},
		Pages: []PageResult{
			// ページ番号の昇順に整列されることを確かめるため順不同で渡す
			{
				Page:     3,
				Sections: []PageSection{{Text: "経路選択器は関連度スコアを計算する"}},
				Usage:    bedrock.Usage{InputTokens: 300, OutputTokens: 30},
			},
			{
				Page:     1,
				Title:    "疎な注意経路選択による長文脈言語モデルの高速化",
				Authors:  []domain.Author{{Name: "田中 藍子"}},
				Abstract: "本稿ではクエリヘッドごとに疎な鍵値対を選択する経路選択機構を提案する",
				Sections: []PageSection{{Level: 1, Heading: "1 はじめに", Text: "近年の言語モデルは文脈長を拡大してきた"}},
				ModelID:  sampleModelID,
				Usage:    bedrock.Usage{InputTokens: 100, OutputTokens: 10},
			},
			{
				Page:     4,
				Sections: []PageSection{{Level: 1, Heading: "3 実験", Text: "提案手法を 3 つのベンチマークで評価した"}},
				Usage:    bedrock.Usage{InputTokens: 400, OutputTokens: 40},
			},
			{
				Page:     2,
				Sections: []PageSection{{Level: 1, Heading: "2 提案手法", Text: "クエリベクトルと鍵ベクトル集合が与えられたとき、"}},
				Usage:    bedrock.Usage{InputTokens: 200, OutputTokens: 20},
			},
		},
	})

	if doc.SchemaVersion != domain.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", doc.SchemaVersion, domain.SchemaVersion)
	}
	if doc.Source.Language != domain.LanguageJapanese || doc.Source.PageCount != 4 {
		t.Errorf("Source = %+v (入力をそのまま載せること)", doc.Source)
	}
	if doc.Metadata.Title != "疎な注意経路選択による長文脈言語モデルの高速化" {
		t.Errorf("Metadata.Title = %q (1 ページ目を優先すること)", doc.Metadata.Title)
	}

	if len(doc.Sections) != 3 {
		t.Fatalf("Sections の件数 = %d, want 3\n%+v", len(doc.Sections), doc.Sections)
	}
	if doc.Sections[0].Heading != "1 はじめに" || !reflect.DeepEqual(doc.Sections[0].Pages, []int{1}) {
		t.Errorf("Sections[0] = %+v", doc.Sections[0])
	}

	// ページ 3 は見出しなしで始まるため、ページ 2 の章の続きとして 1 つの Section に畳まれる
	cross := doc.Sections[1]
	if cross.Heading != "2 提案手法" {
		t.Errorf("Sections[1].Heading = %q, want 2 提案手法", cross.Heading)
	}
	if !reflect.DeepEqual(cross.Pages, []int{2, 3}) {
		t.Errorf("Sections[1].Pages = %v, want [2 3]", cross.Pages)
	}
	if want := "クエリベクトルと鍵ベクトル集合が与えられたとき、経路選択器は関連度スコアを計算する"; cross.Text != want {
		t.Errorf("Sections[1].Text = %q,\nwant %q (日本語の連結に空白を入れないこと)", cross.Text, want)
	}

	// 見出しで始まるページは前ページの続きにせず新しい章を開始する
	if doc.Sections[2].Heading != "3 実験" || !reflect.DeepEqual(doc.Sections[2].Pages, []int{4}) {
		t.Errorf("Sections[2] = %+v", doc.Sections[2])
	}

	if doc.Provenance.Route != domain.RouteBedrock {
		t.Errorf("Provenance.Route = %q, want bedrock", doc.Provenance.Route)
	}
	cost := doc.Provenance.Cost
	if cost.BedrockInputTokens != 1000 || cost.BedrockOutputTokens != 100 {
		t.Errorf("Cost のトークン数 = %+v, want input=1000 output=100", cost)
	}
	if cost.BedrockModel != sampleModelID {
		t.Errorf("Cost.BedrockModel = %q, want %q", cost.BedrockModel, sampleModelID)
	}
	if cost.TextractPages != 0 || cost.TextractFeatures != nil {
		t.Errorf("経路 B に Textract のコストが入っている: %+v", cost)
	}
	if doc.Provenance.Warnings != nil {
		t.Errorf("Warnings = %v, want なし", doc.Provenance.Warnings)
	}
	if !doc.Provenance.ExtractedAt.IsZero() || doc.Provenance.DurationMs != 0 {
		t.Errorf("ExtractedAt と DurationMs は呼び出し側が埋める: %+v", doc.Provenance)
	}
}

// 見出しの有無とフラグの組み合わせだけで章の開始が機械的に決まることを確かめる
func TestMergeSectionBoundary(t *testing.T) {
	first := PageResult{Page: 1, Sections: []PageSection{{Level: 1, Heading: "1 Introduction", Text: "intro"}}}

	tests := map[string]struct {
		second       PageResult
		wantSections int
		wantPages    []int
		wantText     string
	}{
		"正常系_フラグが欠落し見出しもない場合_前ページの章に連結されること": {
			second:       PageResult{Page: 2, Sections: []PageSection{{Text: "continued"}}},
			wantSections: 1,
			wantPages:    []int{1, 2},
			wantText:     "intro continued",
		},
		"正常系_フラグが true で見出しがない場合_前ページの章に連結されること": {
			second:       PageResult{Page: 2, ContinuesPreviousSection: domain.Ptr(true), Sections: []PageSection{{Text: "continued"}}},
			wantSections: 1,
			wantPages:    []int{1, 2},
			wantText:     "intro continued",
		},
		"正常系_フラグが false で見出しがない場合_見出しなしの章が始まること": {
			second:       PageResult{Page: 2, ContinuesPreviousSection: domain.Ptr(false), Sections: []PageSection{{Text: "standalone"}}},
			wantSections: 2,
			wantPages:    []int{2},
			wantText:     "standalone",
		},
		"正常系_フラグが true でも見出しがある場合_新しい章が始まること": {
			second:       PageResult{Page: 2, ContinuesPreviousSection: domain.Ptr(true), Sections: []PageSection{{Level: 1, Heading: "2 Method", Text: "method"}}},
			wantSections: 2,
			wantPages:    []int{2},
			wantText:     "method",
		},
		"正常系_ページ途中に見出しなしブロックが続く場合_同じページ番号を重複させずに連結すること": {
			second: PageResult{Page: 2, ContinuesPreviousSection: domain.Ptr(false), Sections: []PageSection{
				{Level: 1, Heading: "2 Method", Text: "method"},
				{Text: "more"},
			}},
			wantSections: 2,
			wantPages:    []int{2},
			wantText:     "method more",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			doc := Merge(MergeInput{
				Source: domain.Source{PageCount: 2},
				Pages:  []PageResult{first, tt.second},
			})

			if len(doc.Sections) != tt.wantSections {
				t.Fatalf("Sections の件数 = %d, want %d\n%+v", len(doc.Sections), tt.wantSections, doc.Sections)
			}
			last := doc.Sections[len(doc.Sections)-1]
			if !reflect.DeepEqual(last.Pages, tt.wantPages) {
				t.Errorf("末尾の Section の Pages = %v, want %v", last.Pages, tt.wantPages)
			}
			if last.Text != tt.wantText {
				t.Errorf("末尾の Section の Text = %q, want %q", last.Text, tt.wantText)
			}
		})
	}
}

// ページ冒頭が前ページ末尾の続きである参照文献を 1 件に畳めることを確かめる
func TestMergeReferencesAcrossPages(t *testing.T) {
	head := PageResult{Page: 1, References: []PageReference{
		{Raw: "Vaswani, A. et al. Attention Is All You", Title: "Attention Is All You Need"},
	}}

	tests := map[string]struct {
		tail     PageResult
		wantRefs int
		wantRaw  string
		wantDOI  string
		wantYear int
	}{
		"正常系_続きのフラグが立つ場合_前ページ末尾の 1 件へ連結されること": {
			tail: PageResult{Page: 2, ContinuesPreviousReference: domain.Ptr(true), References: []PageReference{
				{Raw: "Need. NeurIPS, 2017.", Year: 2017, DOI: "10.1000/example.0001"},
			}},
			wantRefs: 1,
			wantRaw:  "Vaswani, A. et al. Attention Is All You Need. NeurIPS, 2017.",
			wantDOI:  "10.1000/example.0001",
			wantYear: 2017,
		},
		"正常系_フラグが欠落する場合_別の 1 件として扱われること": {
			tail: PageResult{Page: 2, References: []PageReference{
				{Raw: "Devlin, J. et al. BERT. NAACL, 2019.", Year: 2019},
			}},
			wantRefs: 2,
			wantRaw:  "Devlin, J. et al. BERT. NAACL, 2019.",
			wantYear: 2019,
		},
		"正常系_フラグが false の場合_別の 1 件として扱われること": {
			tail: PageResult{Page: 2, ContinuesPreviousReference: domain.Ptr(false), References: []PageReference{
				{Raw: "Devlin, J. et al. BERT. NAACL, 2019."},
			}},
			wantRefs: 2,
			wantRaw:  "Devlin, J. et al. BERT. NAACL, 2019.",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			doc := Merge(MergeInput{Source: domain.Source{PageCount: 2}, Pages: []PageResult{head, tt.tail}})

			if len(doc.References) != tt.wantRefs {
				t.Fatalf("References の件数 = %d, want %d\n%+v", len(doc.References), tt.wantRefs, doc.References)
			}
			last := doc.References[len(doc.References)-1]
			if last.Raw != tt.wantRaw {
				t.Errorf("Raw = %q, want %q", last.Raw, tt.wantRaw)
			}
			if last.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", last.Year, tt.wantYear)
			}
			switch {
			case tt.wantDOI == "":
				if last.DOI != nil {
					t.Errorf("DOI = %q, want nil", *last.DOI)
				}
			case last.DOI == nil:
				t.Errorf("DOI = nil, want %q", tt.wantDOI)
			case *last.DOI != tt.wantDOI:
				t.Errorf("DOI = %q, want %q", *last.DOI, tt.wantDOI)
			}
		})
	}
}

// 日本語の参照文献を跨いだ場合に原本にない空白が入らないことを確かめる
func TestMergeReferencesJapanese(t *testing.T) {
	doc := Merge(MergeInput{
		Source: domain.Source{Language: domain.LanguageJapanese, PageCount: 2},
		Pages: []PageResult{
			{Page: 1, References: []PageReference{{Raw: "田中 藍子. 疎な注意経路選択による"}}},
			{Page: 2, ContinuesPreviousReference: domain.Ptr(true), References: []PageReference{{Raw: "長文脈言語モデルの高速化. 人工知能学会, 2026."}}},
		},
	})

	if len(doc.References) != 1 {
		t.Fatalf("References の件数 = %d, want 1", len(doc.References))
	}
	if want := "田中 藍子. 疎な注意経路選択による長文脈言語モデルの高速化. 人工知能学会, 2026."; doc.References[0].Raw != want {
		t.Errorf("Raw = %q,\nwant %q", doc.References[0].Raw, want)
	}
}

// Map の一部が失敗してページが欠けても中断せず、警告に残して続行することを確かめる
func TestMergeRecordsIncompletePages(t *testing.T) {
	doc := Merge(MergeInput{
		Source: domain.Source{PageCount: 4},
		Pages: []PageResult{
			{Page: 1, Sections: []PageSection{{Level: 1, Heading: "1 Introduction", Text: "intro"}}},
			{Page: 4, Sections: []PageSection{{Level: 1, Heading: "4 Conclusion", Text: "done"}}},
			{Page: 4, Sections: []PageSection{{Level: 1, Heading: "重複した再実行の結果", Text: "dup"}}},
			{Page: 0, Sections: []PageSection{{Text: "ページ番号が無い結果"}}},
		},
	})

	// 欠落しても結合そのものは成立する
	if len(doc.Sections) != 2 {
		t.Fatalf("Sections の件数 = %d, want 2\n%+v", len(doc.Sections), doc.Sections)
	}

	got := strings.Join(doc.Provenance.Warnings, "\n")
	for _, want := range []string{"page=2", "page=3", "重複", "不正"} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings に %q が無い:\n%s", want, got)
		}
	}
	if len(doc.Provenance.Warnings) != 4 {
		t.Errorf("Warnings の件数 = %d, want 4\n%s", len(doc.Provenance.Warnings), got)
	}
}

// 経路 B が座標を持たないことが出力の形にも表れることを確かめる (#7 の決定)
func TestMergeOmitsBBox(t *testing.T) {
	doc := Merge(MergeInput{
		Source: domain.Source{PageCount: 2},
		Pages: []PageResult{
			{Page: 2, Figures: []PageFigure{{Label: "図 1", Caption: "提案手法の全体像"}}},
			{Page: 1, Tables: []PageTable{{Label: "表 1", Caption: "比較"}}},
		},
	})

	if len(doc.Figures) != 1 || doc.Figures[0].ID != "図 1" || doc.Figures[0].Page != 2 {
		t.Errorf("Figures = %+v (label を id へ、ページ番号を page へ写すこと)", doc.Figures)
	}
	if doc.Figures[0].BBox != nil {
		t.Errorf("Figures[0].BBox = %v, want nil", doc.Figures[0].BBox)
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got struct {
		Figures []map[string]json.RawMessage `json:"figures"`
		Tables  []map[string]json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := got.Figures[0]["bbox"]; ok {
		t.Error("figures に bbox キーが残っている")
	}
	if _, ok := got.Tables[0]["bbox"]; ok {
		t.Error("tables に bbox キーが残っている")
	}
	// 経路間で差分を取るため、要素が無くても配列として出力する
	if string(got.Tables[0]["header"]) != "[]" || string(got.Tables[0]["rows"]) != "[]" {
		t.Errorf("tables の header と rows が配列になっていない: %s", encoded)
	}
}

// ページ結果が 1 件も揃わなくても空の Document を返せることを確かめる
func TestMergeWithoutPages(t *testing.T) {
	doc := Merge(MergeInput{JobID: "01JB9Q4T7C2E8HXM5KRV0YWN3B", Source: domain.Source{PageCount: 2}})

	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"sections", "figures", "tables", "references"} {
		if string(got[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, got[key])
		}
	}
	if len(doc.Provenance.Warnings) != 2 {
		t.Errorf("Warnings の件数 = %d, want 2", len(doc.Provenance.Warnings))
	}
}

func TestJoinFragments(t *testing.T) {
	tests := map[string]struct {
		a, b string
		want string
	}{
		"正常系_両端が ASCII の場合_空白で繋がること": {a: "state of the", b: "art", want: "state of the art"},
		"正常系_左端が日本語の場合_空白を入れずに繋がること": {a: "経路選択を", b: "proposed", want: "経路選択をproposed"},
		"正常系_右端が日本語の場合_空白を入れずに繋がること": {a: "we propose", b: "経路選択", want: "we propose経路選択"},
		"正常系_両端が日本語の場合_空白を入れずに繋がること": {a: "疎な注意経路", b: "選択機構", want: "疎な注意経路選択機構"},
		"境界値_左が空の場合_右だけが返ること":        {a: "", b: "art", want: "art"},
		"境界値_右が空の場合_左だけが返ること":        {a: "state", b: "", want: "state"},
		"境界値_両方が空の場合_空文字が返ること":       {a: "", b: "", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := joinFragments(tt.a, tt.b); got != tt.want {
				t.Errorf("joinFragments(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
