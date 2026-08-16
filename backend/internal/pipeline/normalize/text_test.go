package normalize

import (
	"testing"

	"github.com/tamaco489/folio/backend/internal/domain"
)

func TestCollapseSpace(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"正常系_前後の空白がある場合_除かれること":            {in: "  Sparse Attention \t", want: "Sparse Attention"},
		"正常系_連続する空白がある場合_1 つになること":         {in: "Sparse   Attention\t\tRouting", want: "Sparse Attention Routing"},
		"正常系_英語の途中に改行がある場合_空白で繋ぐこと":        {in: "Sparse Attention Routing for\nLong-Context Language Models", want: "Sparse Attention Routing for Long-Context Language Models"},
		"正常系_日本語の途中に改行がある場合_空白を挟まず繋ぐこと":    {in: "疎な注意経路選択による\n長文脈言語モデルの高速化", want: "疎な注意経路選択による長文脈言語モデルの高速化"},
		"正常系_日本語と英数字の境界に改行がある場合_空白で繋ぐこと":   {in: "文脈長を\n2K トークンから", want: "文脈長を 2K トークンから"},
		"正常系_日本語の途中に空白がある場合_1 つの空白として残ること": {in: "田中  藍子", want: "田中 藍子"},
		"正常系_全角スペースの場合_空白として揃えること":         {in: "田中　藍子", want: "田中 藍子"},
		"正常系_CRLF を含む場合_空白として揃えること":        {in: "for\r\nLong", want: "for Long"},
		"境界値_空文字の場合_空文字のままであること":           {in: "", want: ""},
		"境界値_空白だけの場合_空文字になること":             {in: " \n\t ", want: ""},
		"境界値_全角英数字を含む場合_文字種は変換しないこと":       {in: "Ｈ２Ｏ と H2O", want: "Ｈ２Ｏ と H2O"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := collapseSpace(tt.in); got != tt.want {
				t.Errorf("collapseSpace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeText(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"正常系_段落の区切りの空行がある場合_改行を保つこと": {in: "First paragraph.\n\nSecond paragraph.", want: "First paragraph.\n\nSecond paragraph."},
		"正常系_行末に空白がある場合_除かれること":      {in: "line one  \nline two\t\n", want: "line one\nline two"},
		"正常系_CRLF の場合_LF に統一されること":   {in: "line one\r\nline two\rline three", want: "line one\nline two\nline three"},
		"正常系_前後に空行がある場合_除かれること":      {in: "\n\n  body\n\n", want: "body"},
		"正常系_行内の連続する空白がある場合_変えないこと":  {in: "a  b", want: "a  b"},
		"正常系_行頭の空白がある場合_変えないこと":      {in: "first\n  indented", want: "first\n  indented"},
		"境界値_空文字の場合_空文字のままであること":     {in: "", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeText(tt.in); got != tt.want {
				t.Errorf("normalizeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// 各フィールドにどの空白正規化を掛けるかを 1 本の文書で確かめる
func TestNormalizeAppliesTextRulesPerField(t *testing.T) {
	got := Normalize(domain.Document{
		Metadata: domain.Metadata{
			Title:    " Sparse\nAttention ",
			Authors:  []domain.Author{{Name: " Aiko  Tanaka ", Affiliation: "Tohoku\nUniversity", Email: " a@example.ac.jp "}},
			Abstract: "Line one\nline two.",
			Keywords: []string{" sparse  attention ", "", "long context"},
			Venue:    " arXiv  preprint ",
		},
		Sections: []domain.Section{{Level: 1, Heading: " 1  Introduction ", Text: "para one  \n\npara  two "}},
		Figures:  []domain.Figure{{ID: " Figure  1 ", Caption: "Overview\nof the model.", Page: 3}},
		Tables: []domain.Table{{
			ID:      " Table 1 ",
			Caption: " Results ",
			Header:  [][]string{{" Model ", "Acc  uracy"}},
			Rows:    [][]string{{" Ours ", " 92.6 "}},
		}},
	}, WithClock(fixedClock))

	if got.Metadata.Title != "Sparse Attention" {
		t.Errorf("Metadata.Title = %q", got.Metadata.Title)
	}
	if a := got.Metadata.Authors[0]; a.Name != "Aiko Tanaka" || a.Affiliation != "Tohoku University" || a.Email != "a@example.ac.jp" {
		t.Errorf("Metadata.Authors[0] = %+v", a)
	}
	if got.Metadata.Abstract != "Line one line two." {
		t.Errorf("Metadata.Abstract = %q (要旨は 1 段落として改行を空白に揃えること)", got.Metadata.Abstract)
	}
	if len(got.Metadata.Keywords) != 2 || got.Metadata.Keywords[0] != "sparse attention" {
		t.Errorf("Metadata.Keywords = %q", got.Metadata.Keywords)
	}
	if got.Metadata.Venue != "arXiv preprint" {
		t.Errorf("Metadata.Venue = %q", got.Metadata.Venue)
	}
	if got.Sections[0].Heading != "1 Introduction" {
		t.Errorf("Sections[0].Heading = %q", got.Sections[0].Heading)
	}
	if got.Sections[0].Text != "para one\n\npara  two" {
		t.Errorf("Sections[0].Text = %q (本文の改行と行内の空白は保つこと)", got.Sections[0].Text)
	}
	if f := got.Figures[0]; f.ID != "Figure 1" || f.Caption != "Overview of the model." {
		t.Errorf("Figures[0] = %+v", f)
	}
	tbl := got.Tables[0]
	if tbl.ID != "Table 1" || tbl.Caption != "Results" {
		t.Errorf("Tables[0] = %+v", tbl)
	}
	if tbl.Header[0][0] != "Model" || tbl.Header[0][1] != "Acc  uracy" {
		t.Errorf("Tables[0].Header = %q (セルは前後の空白だけ除くこと)", tbl.Header)
	}
	if tbl.Rows[0][0] != "Ours" || tbl.Rows[0][1] != "92.6" {
		t.Errorf("Tables[0].Rows = %q", tbl.Rows)
	}
}
