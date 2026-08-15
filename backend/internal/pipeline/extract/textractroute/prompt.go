package textractroute

import (
	"fmt"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// systemPrompt は出力させる JSON の形を指示する
//
// フィールド名は domain.Metadata / domain.Section / domain.Reference の json タグに合わせている
// 図と表は Read が座標付きで復元済みであり、モデルに作らせると行列も座標も失われるため要求しない
// Textract は日本語に対応しないためこの経路には英語の論文しか流れてこない。指示も英語で書く
const systemPrompt = `You turn the layout output of an English academic paper into one JSON object.

Rules:
- Reply with the JSON object only. No prose and no markdown code fence.
- Use only what the input contains. Never invent a value.
- Use "" for a missing string, [] for a missing array, 0 for a missing number and null for a missing doi.
- Keep the original wording. Do not translate, summarise or reformat.
- Do not emit figures or tables. They are extracted separately.

Schema:
{
  "title": string,
  "authors": [{"name": string, "affiliation": string, "email": string}],
  "abstract": string,
  "keywords": [string],
  "venue": string,
  "year": number,
  "sections": [{"level": number, "heading": string, "text": string, "pages": [number]}],
  "references": [{"raw": string, "title": string, "authors": [string], "year": number, "venue": string, "doi": string}]
}

Notes:
- "level" is 1 for a top level section and grows with nesting.
- "pages" lists every page the section body spans, in ascending order.
- "raw" is the reference entry exactly as printed.`

// userPrompt は Read が組み立てたテキストに読み方の説明を添える
func userPrompt(r *Reading) string {
	return fmt.Sprintf(`Layout output of a %d page paper.
Each block is tagged with the Textract layout type it came from and tables are rendered as markdown.
Convert it into the JSON object described in the system prompt.

%s`, r.PageCount, r.Text())
}

// structured はモデルに生成させる JSON
//
// domain の json タグに揃えているが、Section だけは domain と形が違う (下記 Page を参照)
type structured struct {
	Title      string             `json:"title"`
	Authors    []domain.Author    `json:"authors"`
	Abstract   string             `json:"abstract"`
	Keywords   []string           `json:"keywords"`
	Venue      string             `json:"venue"`
	Year       int                `json:"year"`
	Sections   []wireSection      `json:"sections"`
	References []domain.Reference `json:"references"`
}

// wireSection は domain.Section に対応するモデルの出力
type wireSection struct {
	Level   int    `json:"level"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
	Pages   []int  `json:"pages"`

	// Page は pages の代わりに単数のページ番号を返す応答を受けるための予備
	//
	// testdata/bedrock/2301.07041-textract.json が page を用いているため残している (実レスポンスへの差し替え時に揃える)
	Page int `json:"page"`
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

func (s structured) sections() []domain.Section {
	out := make([]domain.Section, 0, len(s.Sections))
	for _, w := range s.Sections {
		pages := w.Pages
		if len(pages) == 0 && w.Page > 0 {
			pages = []int{w.Page}
		}
		out = append(out, domain.Section{
			Level:   w.Level,
			Heading: w.Heading,
			Text:    w.Text,
			Pages:   pages,
		})
	}
	return out
}

func (s structured) references() []domain.Reference {
	if s.References == nil {
		return []domain.Reference{}
	}
	return s.References
}
