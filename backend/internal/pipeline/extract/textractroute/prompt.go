package textractroute

import (
	"fmt"
	"slices"
	"strings"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// systemPrompt は出力の読み方とルールを指示する (形は paperTool のスキーマで渡す)
//
// 本文と参考文献の原文はモデルに書かせず、入力の要素番号 (#n) で指させて Go 側が Reading から組み立てる
//   - 全文を書き出させると出力がページ数に比例して増え、19 ページの論文で 24K トークンの上限を超えた
//   - 図と表は Read が座標付きで復元済みであり、モデルに作らせると行列も座標も失われるため要求しない
//
// Textract は日本語に対応しないためこの経路には英語の論文しか流れてこない。指示も英語で書く
const systemPrompt = `You turn the layout output of an English academic paper into structured data.

Record the result by calling the structure_paper tool. Its input schema defines the keys.

Rules:
- Use only what the input contains. Never invent a value.
- Use "" for a missing string, [] for a missing array, 0 for a missing number and null for a missing doi.
- Never copy body text or reference entries. Refer to input elements by the number after "#" instead.
- Do not emit figures or tables. They are extracted separately.

Notes:
- "level" is 1 for a top level section and grows with nesting.
- "from" and "to" are the numbers of the first and last body elements of the section, inclusive. Do not include the heading element. A section with no body has "from" and "to" set to -1.
- "element" is the number of the element that holds one reference entry. Emit one object per entry.`

// userPrompt は Read が組み立てたテキストに読み方の説明を添える
func userPrompt(r *Reading) string {
	return fmt.Sprintf(`Layout output of a %d page paper.
Each element starts with "#" followed by its number and the Textract layout type it came from. Tables are rendered as markdown.
Record it with the structure_paper tool.

%s`, r.PageCount, r.Text())
}

// structured はモデルに生成させる JSON
//
// domain の json タグに揃えているが、Section と Reference は本文を持たず要素番号で指す (wireSection, wireReference)
type structured struct {
	Title      string          `json:"title"`
	Authors    []domain.Author `json:"authors"`
	Abstract   string          `json:"abstract"`
	Keywords   []string        `json:"keywords"`
	Venue      string          `json:"venue"`
	Year       int             `json:"year"`
	Sections   []wireSection   `json:"sections"`
	References []wireReference `json:"references"`
}

// wireSection は domain.Section に対応するモデルの出力
//
// From と To は Reading.Elements の添字で、本文はその範囲から Go 側が組み立てる
type wireSection struct {
	Level   int    `json:"level"`
	Heading string `json:"heading"`
	From    int    `json:"from"`
	To      int    `json:"to"`
}

// wireReference は domain.Reference に対応するモデルの出力
//
// Raw は Element が指す要素の文字列から取るが、Textract が 1 要素に複数件を詰めた出力でモデルが書き下す場合に備えて Raw も受ける
type wireReference struct {
	Element *int     `json:"element"`
	Raw     string   `json:"raw"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Year    int      `json:"year"`
	Venue   string   `json:"venue"`
	DOI     *string  `json:"doi"`
}

func (s structured) metadata() domain.Metadata {
	m := domain.Metadata{
		Title:    s.Title,
		Authors:  s.Authors,
		Abstract: s.Abstract,
		Keywords: s.Keywords,
		Venue:    s.Venue,
		Year:     s.Year,
	}
	if m.Authors == nil {
		m.Authors = []domain.Author{}
	}
	return m
}

// sections は from..to の本文要素を連結して節を組み立てる
//
// 範囲が不正 (逆順、範囲外) な節は本文を空にし、その旨を警告として返す (見出しは残して章立てを壊さない)
func (s structured) sections(r *Reading) ([]domain.Section, []string) {
	out := make([]domain.Section, 0, len(s.Sections))
	var warns []string
	for i, w := range s.Sections {
		sec := domain.Section{Level: w.Level, Heading: w.Heading, Pages: []int{}}
		switch {
		case w.From == -1 && w.To == -1:
		case w.From < 0 || w.To >= len(r.Elements) || w.From > w.To:
			warns = append(warns, fmt.Sprintf("sections[%d] (%q): 本文の範囲 #%d-#%d が不正なため本文を空にした (要素は %d 件)", i, w.Heading, w.From, w.To, len(r.Elements)))
		default:
			var parts []string
			for _, e := range r.Elements[w.From : w.To+1] {
				if !e.isBody() || e.Text == "" {
					continue
				}
				parts = append(parts, e.Text)
				if !slices.Contains(sec.Pages, e.Page) {
					sec.Pages = append(sec.Pages, e.Page)
				}
			}
			sec.Text = strings.Join(parts, "\n\n")
			slices.Sort(sec.Pages)
		}
		out = append(out, sec)
	}
	return out, warns
}

// references は element が指す要素の文字列を raw にする
func (s structured) references(r *Reading) ([]domain.Reference, []string) {
	out := make([]domain.Reference, 0, len(s.References))
	var warns []string
	for i, w := range s.References {
		ref := domain.Reference{Raw: strings.TrimSpace(w.Raw), Title: w.Title, Authors: w.Authors, Year: w.Year, Venue: w.Venue, DOI: w.DOI}
		if w.Element != nil {
			if n := *w.Element; n >= 0 && n < len(r.Elements) {
				ref.Raw = r.Elements[n].Text
			} else {
				warns = append(warns, fmt.Sprintf("references[%d] (%q): 要素 #%d が範囲外のため raw を特定できなかった (要素は %d 件)", i, w.Title, n, len(r.Elements)))
			}
		}
		out = append(out, ref)
	}
	return out, warns
}

// isBody は節の本文として連結する要素かを返す (見出し、ヘッダー、フッター、ページ番号、図、表は除く)
func (e Element) isBody() bool {
	switch e.Type {
	case awstextracttypes.BlockTypeLayoutText, awstextracttypes.BlockTypeLayoutList, awstextracttypes.BlockTypeLine:
		return e.TableID == ""
	}
	return false
}
