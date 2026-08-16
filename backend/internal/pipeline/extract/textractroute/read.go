// Package textractroute は経路 A (Textract で読み取り、Bedrock で解釈する) の抽出を担う
//
// 処理は 2 段に分かれる
//   - Read: Textract が返した Block を読み順と表構造へ復元する (Bedrock を呼ばない純ロジック)
//   - Extractor.Extract: Read の結果を Bedrock へ渡し domain.Document へ落とす
//
// Textract の呼び出し自体は行わない。取得済みの Block を受け取るだけであるため、FeatureTypes の組み合わせに依存するのはどの BlockType を解釈できるかだけになる
package textractroute

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/domain"
)

// layoutTypes は読み順の復元に用いる LAYOUT ブロック
//
// FeatureTypes に LAYOUT を含めずに取得した出力でも落ちないよう、該当が 1 件も無いページは LINE で代替する
var layoutTypes = map[awstextracttypes.BlockType]struct{}{
	awstextracttypes.BlockTypeLayoutTitle:         {},
	awstextracttypes.BlockTypeLayoutHeader:        {},
	awstextracttypes.BlockTypeLayoutFooter:        {},
	awstextracttypes.BlockTypeLayoutSectionHeader: {},
	awstextracttypes.BlockTypeLayoutPageNumber:    {},
	awstextracttypes.BlockTypeLayoutList:          {},
	awstextracttypes.BlockTypeLayoutFigure:        {},
	awstextracttypes.BlockTypeLayoutTable:         {},
	awstextracttypes.BlockTypeLayoutKeyValue:      {},
	awstextracttypes.BlockTypeLayoutText:          {},
}

// Element は読み順に並べたレイアウト要素 1 件
type Element struct {
	Type       awstextracttypes.BlockType // Type は由来の BlockType (LAYOUT_* か、代替に用いた LINE)
	Text       string                     // Text は子孫の LINE と WORD を連結したもの
	Page       int                        // Page は 1 始まりのページ番号
	Column     int                        // Column は段番号 (1 が左段、2 が右段、0 は段を跨ぐ全幅要素)
	BBox       domain.BBox                // BBox は正規化座標の [x0, y0, x1, y1]
	Confidence float64                    // Confidence は Textract の確信度を 0.0 - 1.0 に正規化した値
	TableID    string                     // TableID は LAYOUT_TABLE に対応づけた表の ID
}

// Table は復元した表と Textract の確信度
//
// domain.Table には確信度を持つ場がないため、provenance.confidence を組み立てるまでの間ここで保持する
type Table struct {
	Data       domain.Table
	Confidence float64
}

// Figure は LAYOUT_FIGURE から得た図と Textract の確信度
type Figure struct {
	Data       domain.Figure
	Confidence float64
}

// Reading は Block を解釈した結果
type Reading struct {
	PageCount int       // PageCount は原本のページ数 (Textract の課金単位)
	Elements  []Element // Elements は読み順に並べたレイアウト要素
	Tables    []Table
	Figures   []Figure
	Warnings  []string // Warnings は推定で補った箇所 (そのまま domain.Provenance.Warnings に載せる)
}

// Read は Textract の Block を読み順と表構造へ復元する
func Read(res *textract.AnalysisResult) (*Reading, error) {
	if res == nil || len(res.Blocks) == 0 {
		return nil, ErrEmptyAnalysis
	}

	index := make(map[string]*awstextracttypes.Block, len(res.Blocks))
	for i := range res.Blocks {
		if id := aws.ToString(res.Blocks[i].Id); id != "" {
			index[id] = &res.Blocks[i]
		}
	}

	r := &Reading{PageCount: pageCount(res)}

	// 表は LAYOUT_TABLE との対応づけに使うため、レイアウト要素より先に組み立てる
	for i := range res.Blocks {
		if res.Blocks[i].BlockType != awstextracttypes.BlockTypeTable {
			continue
		}
		tbl, warns := buildTable(index, &res.Blocks[i], fmt.Sprintf("table-%d", len(r.Tables)+1))
		r.Tables = append(r.Tables, tbl)
		r.Warnings = append(r.Warnings, warns...)
	}

	byPage := map[int][]Element{}
	hasLayout := map[int]bool{}
	var pages []int
	addPage := func(p int) {
		if _, seen := byPage[p]; !seen {
			pages = append(pages, p)
			byPage[p] = nil
		}
	}

	for i := range res.Blocks {
		b := &res.Blocks[i]
		if _, ok := layoutTypes[b.BlockType]; !ok {
			continue
		}
		p := blockPage(b)
		addPage(p)
		hasLayout[p] = true
		byPage[p] = append(byPage[p], newElement(index, b))
	}
	for i := range res.Blocks {
		b := &res.Blocks[i]
		if b.BlockType != awstextracttypes.BlockTypeLine || hasLayout[blockPage(b)] {
			continue
		}
		p := blockPage(b)
		addPage(p)
		byPage[p] = append(byPage[p], newElement(index, b))
	}
	if len(pages) == 0 && len(r.Tables) == 0 {
		return nil, ErrEmptyAnalysis
	}

	slices.Sort(pages)
	for _, p := range pages {
		ordered, reordered := orderPage(byPage[p])
		if reordered {
			r.Warnings = append(r.Warnings, fmt.Sprintf("page %d: 二段組と判定し Textract が返した読み順を左段から右段へ並べ直した", p))
		}
		r.Elements = append(r.Elements, ordered...)
	}

	r.link()
	return r, nil
}

// link は LAYOUT_TABLE を復元済みの表へ、LAYOUT_FIGURE を図へ対応づける
//
// 図と表の ID は読み順で振るため、対応づけはページ内の並べ替えが済んでから行う
func (r *Reading) link() {
	used := make([]bool, len(r.Tables))
	for i := range r.Elements {
		e := &r.Elements[i]
		switch e.Type {
		case awstextracttypes.BlockTypeLayoutTable:
			t := r.matchTable(e, used)
			if t < 0 {
				continue
			}
			used[t] = true
			e.TableID = r.Tables[t].Data.ID
			if r.Tables[t].Data.Caption == "" {
				r.Tables[t].Data.Caption = r.captionNear(i, "table")
			}
		case awstextracttypes.BlockTypeLayoutFigure:
			fig := domain.Figure{
				ID:      fmt.Sprintf("figure-%d", len(r.Figures)+1),
				Caption: r.captionNear(i, "figure", "fig."),
				Page:    e.Page,
				BBox:    e.BBox,
			}
			if fig.Caption == "" {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s: キャプションを特定できなかった", fig.ID))
			}
			r.Figures = append(r.Figures, Figure{Data: fig, Confidence: e.Confidence})
		}
	}
}

// matchTable は LAYOUT_TABLE と最も重なる未使用の表の位置を返す (見つからない場合は -1)
func (r *Reading) matchTable(e *Element, used []bool) int {
	best, area := -1, 0.0
	for i := range r.Tables {
		if used[i] || r.Tables[i].Data.Page != e.Page {
			continue
		}
		if a := overlapArea(e.BBox, r.Tables[i].Data.BBox); a > area {
			best, area = i, a
		}
	}
	return best
}

// captionNear は図表の直後と直前の要素からキャプションを拾う
//
// Textract は図のキャプションを本文と同じ LAYOUT_TEXT で返すため、接頭辞で見分けるほかない
func (r *Reading) captionNear(i int, prefixes ...string) string {
	for _, j := range []int{i + 1, i - 1} {
		if j < 0 || j >= len(r.Elements) || r.Elements[j].Page != r.Elements[i].Page {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(r.Elements[j].Text))
		for _, p := range prefixes {
			if strings.HasPrefix(lower, p) {
				return strings.TrimSpace(r.Elements[j].Text)
			}
		}
	}
	return ""
}

// Text は Bedrock へ渡すためのテキスト表現を組み立てる
//
// 要素には由来の BlockType を付け、表は Markdown で埋め込む
func (r *Reading) Text() string {
	var sb strings.Builder
	rendered := map[string]bool{}
	page := 0

	flush := func(p int) {
		for _, t := range r.Tables {
			if t.Data.Page != p || rendered[t.Data.ID] {
				continue
			}
			rendered[t.Data.ID] = true
			fmt.Fprintf(&sb, "[TABLE %s]\n%s\n", t.Data.ID, t.markdown())
		}
	}

	for _, e := range r.Elements {
		if e.Page != page {
			flush(page)
			page = e.Page
			fmt.Fprintf(&sb, "## page %d\n\n", page)
		}
		if e.TableID != "" {
			for _, t := range r.Tables {
				if t.Data.ID == e.TableID {
					rendered[e.TableID] = true
					fmt.Fprintf(&sb, "[TABLE %s]\n%s\n", e.TableID, t.markdown())
				}
			}
			continue
		}
		if e.Text == "" {
			continue
		}
		fmt.Fprintf(&sb, "[%s] %s\n\n", e.tag(), e.Text)
	}
	flush(page)

	// レイアウト要素が 1 件も無い場合でも、表だけは残らず渡す
	for _, t := range r.Tables {
		if rendered[t.Data.ID] {
			continue
		}
		fmt.Fprintf(&sb, "[TABLE %s]\n%s\n", t.Data.ID, t.markdown())
	}
	return sb.String()
}

// Confidence は Textract の確信度から provenance.confidence の材料を組み立てる
//
// Bedrock が解釈する著者・要旨・参考文献には Textract 側の裏付けが無いため空のままとする
func (r *Reading) Confidence() domain.Confidence {
	var c domain.Confidence
	var title, sections, tables, figures []float64
	for _, e := range r.Elements {
		switch e.Type {
		case awstextracttypes.BlockTypeLayoutTitle:
			title = append(title, e.Confidence)
		case awstextracttypes.BlockTypeLayoutSectionHeader:
			sections = append(sections, e.Confidence)
		}
	}
	for _, t := range r.Tables {
		tables = append(tables, t.Confidence)
	}
	for _, f := range r.Figures {
		figures = append(figures, f.Confidence)
	}
	for _, s := range []struct {
		values []float64
		dst    **float64
	}{
		{title, &c.Title},
		{sections, &c.Sections},
		{tables, &c.Tables},
		{figures, &c.Figures},
	} {
		if len(s.values) > 0 {
			*s.dst = new(round(mean(s.values)))
		}
	}
	return c
}

func (e Element) tag() string {
	return strings.TrimPrefix(string(e.Type), "LAYOUT_")
}

func newElement(index map[string]*awstextracttypes.Block, b *awstextracttypes.Block) Element {
	return Element{
		Type:       b.BlockType,
		Text:       blockText(index, b, map[string]bool{}),
		Page:       blockPage(b),
		BBox:       toBBox(b.Geometry),
		Confidence: confidenceOf(b),
	}
}

// blockText は子孫の LINE と WORD を読み順に連結する
//
// seen は Relationships に循環がある壊れた出力で無限再帰に陥らないために持ち回る
func blockText(index map[string]*awstextracttypes.Block, b *awstextracttypes.Block, seen map[string]bool) string {
	if b == nil {
		return ""
	}
	id := aws.ToString(b.Id)
	if seen[id] {
		return ""
	}
	seen[id] = true

	if t := aws.ToString(b.Text); t != "" {
		return t
	}
	if b.BlockType == awstextracttypes.BlockTypeSelectionElement {
		return string(b.SelectionStatus)
	}

	var parts []string
	for _, rel := range b.Relationships {
		if rel.Type != awstextracttypes.RelationshipTypeChild {
			continue
		}
		for _, cid := range rel.Ids {
			child := index[cid]
			if child == nil {
				continue
			}
			if t := blockText(index, child, seen); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, " ")
}

// toBBox は Textract の矩形 (左上原点、幅と高さ) を domain.BBox の [x0, y0, x1, y1] へ変換する
//
// float32 から float64 へ広げると 0.12 が 0.11999999731779099 になり出力の差分が読めなくなるため、float32 の有効桁に丸める
func toBBox(g *awstextracttypes.Geometry) domain.BBox {
	if g == nil || g.BoundingBox == nil {
		return nil
	}
	b := g.BoundingBox
	return domain.BBox{
		round(float64(b.Left)),
		round(float64(b.Top)),
		round(float64(b.Left + b.Width)),
		round(float64(b.Top + b.Height)),
	}
}

// round は float32 の有効桁である 6 桁へ丸める
func round(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

func blockPage(b *awstextracttypes.Block) int {
	if p := int(aws.ToInt32(b.Page)); p > 0 {
		return p
	}
	return 1
}

// confidenceOf は Textract の 0 - 100 の確信度を 0.0 - 1.0 へ直す
func confidenceOf(b *awstextracttypes.Block) float64 {
	return round(float64(aws.ToFloat32(b.Confidence)) / 100)
}

// pageCount は課金単位となる原本のページ数を返す (AnalysisResult.Pages はレスポンス数であり別物)
func pageCount(res *textract.AnalysisResult) int {
	if res.DocumentMetadata != nil {
		if n := int(aws.ToInt32(res.DocumentMetadata.Pages)); n > 0 {
			return n
		}
	}
	n := 0
	for i := range res.Blocks {
		n = max(n, blockPage(&res.Blocks[i]))
	}
	return max(n, 1)
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
