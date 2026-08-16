package textractroute

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// cell は行列の復元に必要な範囲だけを取り出したセル
type cell struct {
	row        int
	col        int
	rowSpan    int
	colSpan    int
	text       string
	header     bool
	confidence float64
}

func newCell(index map[string]*awstextracttypes.Block, b *awstextracttypes.Block) cell {
	c := cell{
		row:        int(aws.ToInt32(b.RowIndex)),
		col:        int(aws.ToInt32(b.ColumnIndex)),
		rowSpan:    max(int(aws.ToInt32(b.RowSpan)), 1),
		colSpan:    max(int(aws.ToInt32(b.ColumnSpan)), 1),
		text:       blockText(index, b, map[string]bool{}),
		confidence: confidenceOf(b),
	}
	for _, e := range b.EntityTypes {
		if e == awstextracttypes.EntityTypeColumnHeader {
			c.header = true
		}
	}
	return c
}

// buildTable は TABLE ブロックから行列を復元する
//
// CELL は結合の有無によらず 1 マスずつ返るため、範囲を持つのは MERGED_CELL だけになる
// 結合セルは domain.Table の規約に従い、復元した値を範囲内の全マスへ複製する
func buildTable(index map[string]*awstextracttypes.Block, tbl *awstextracttypes.Block, id string) (Table, []string) {
	var (
		warns    []string
		cells    []cell
		merged   []cell
		title    string
		footer   string
		confs    []float64
		rows     int
		cols     int
		fromMain = confidenceOf(tbl)
	)
	if fromMain > 0 {
		confs = append(confs, fromMain)
	}

	for _, rel := range tbl.Relationships {
		for _, cid := range rel.Ids {
			b := index[cid]
			if b == nil {
				continue
			}
			switch b.BlockType {
			case awstextracttypes.BlockTypeCell:
				cells = append(cells, newCell(index, b))
			case awstextracttypes.BlockTypeMergedCell:
				merged = append(merged, newCell(index, b))
			case awstextracttypes.BlockTypeTableTitle:
				title = joinNonEmpty(title, blockText(index, b, map[string]bool{}))
			case awstextracttypes.BlockTypeTableFooter:
				footer = joinNonEmpty(footer, blockText(index, b, map[string]bool{}))
			}
		}
	}

	for _, c := range append(append([]cell{}, cells...), merged...) {
		rows = max(rows, c.row+c.rowSpan-1)
		cols = max(cols, c.col+c.colSpan-1)
	}

	table := Table{Data: domain.Table{
		ID:      id,
		Caption: firstNonEmpty(title, footer),
		Page:    blockPage(tbl),
		BBox:    toBBox(tbl.Geometry),
		Header:  [][]string{},
		Rows:    [][]string{},
	}}
	if rows == 0 || cols == 0 {
		return table, append(warns, fmt.Sprintf("%s: セルが 1 件も無いため行列を復元できなかった", id))
	}

	grid := make([][]string, rows)
	filled := make([][]bool, rows)
	for r := range grid {
		grid[r] = make([]string, cols)
		filled[r] = make([]bool, cols)
	}
	headerRow := make([]bool, rows)

	place := func(c cell) {
		for r := c.row - 1; r >= 0 && r < min(c.row-1+c.rowSpan, rows); r++ {
			if c.header {
				headerRow[r] = true
			}
			for col := c.col - 1; col >= 0 && col < min(c.col-1+c.colSpan, cols); col++ {
				grid[r][col] = c.text
				filled[r][col] = true
			}
		}
	}
	for _, c := range cells {
		place(c)
		if c.confidence > 0 {
			confs = append(confs, c.confidence)
		}
	}
	// MERGED_CELL は同じ範囲の CELL より後に置き、結合後の値で上書きする
	for _, c := range merged {
		place(c)
	}
	table.Confidence = mean(confs)

	headerCount := 0
	for headerCount < rows && headerRow[headerCount] {
		headerCount++
	}
	for r := headerCount; r < rows; r++ {
		if headerRow[r] {
			warns = append(warns, fmt.Sprintf("%s: 行 %d の COLUMN_HEADER が先頭から連続しないためヘッダーに含めなかった", id, r+1))
		}
	}

	// 多段ヘッダーの 1 段目は結合セルが 1 マスぶんしか返らないことがあり、その場合 MERGED_CELL も付かない
	// 左隣に値があるマスだけを結合の続きとみなして埋め、推定であることを警告に残す
	for r := range headerCount {
		for c := 1; c < cols; c++ {
			if filled[r][c] || grid[r][c-1] == "" {
				continue
			}
			grid[r][c] = grid[r][c-1]
			filled[r][c] = true
			warns = append(warns, fmt.Sprintf("%s: ヘッダー %d 段目の列 %d を左隣のセル結合とみなして推定で復元した", id, r+1, c+1))
		}
	}

	missing := 0
	for r := range rows {
		for c := range cols {
			if !filled[r][c] {
				missing++
			}
		}
	}
	if missing > 0 {
		warns = append(warns, fmt.Sprintf("%s: 欠落した %d 件のセルを空文字で補った", id, missing))
	}

	table.Data.Header = grid[:headerCount]
	table.Data.Rows = grid[headerCount:]
	return table, warns
}

// markdown は Bedrock へ渡すために表を Markdown の行として書き出す
//
// 多段ヘッダーは区切り行の前にヘッダー行を並べて表す
func (t Table) markdown() string {
	var sb strings.Builder
	width := 0
	for _, row := range append(append([][]string{}, t.Data.Header...), t.Data.Rows...) {
		width = max(width, len(row))
	}
	if width == 0 {
		return ""
	}

	writeRow := func(row []string) {
		sb.WriteString("|")
		for c := range width {
			v := ""
			if c < len(row) {
				v = escapeCell(row[c])
			}
			sb.WriteString(" " + v + " |")
		}
		sb.WriteString("\n")
	}

	for _, row := range t.Data.Header {
		writeRow(row)
	}
	if len(t.Data.Header) > 0 {
		sb.WriteString("|")
		for range width {
			sb.WriteString(" --- |")
		}
		sb.WriteString("\n")
	}
	for _, row := range t.Data.Rows {
		writeRow(row)
	}
	return sb.String()
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.Join(strings.Fields(s), " ")
}

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " " + b
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
