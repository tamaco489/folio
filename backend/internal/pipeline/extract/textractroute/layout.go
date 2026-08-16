package textractroute

import (
	"cmp"
	"slices"

	"github.com/tamaco489/folio/backend/internal/domain"
)

const (
	// maxColumnWidth は段に属しうる要素の幅の上限 (ページ幅比)
	//
	// これを超える要素はタイトルや全幅の図表であり、どちらの段にも属さない
	maxColumnWidth = 0.55

	// minGutter は段間の余白と認める幅の下限 (ページ幅比)
	minGutter = 0.02

	// gutterMin と gutterMax は段間の余白を探す範囲 (ページ幅比)
	//
	// 論文の段間はページ中央付近にしか現れないため、範囲を絞って本文中の空白を段間と誤認しないようにする
	gutterMin = 0.3
	gutterMax = 0.7
)

// Columns は 1 ページの段組み
type Columns struct {
	Split     float64 // Split は段の境界となる x 座標 (二段組でない場合は 0)
	TwoColumn bool
}

// DetectColumns は矩形群の縦方向の余白から二段組を判定する
//
// 段に属さない全幅要素を幅で除いたうえで、どの矩形も跨がない最も広い余白を段間とみなす
func DetectColumns(boxes []domain.BBox) Columns {
	narrow := make([]domain.BBox, 0, len(boxes))
	for _, b := range boxes {
		if len(b) == 4 && b[2]-b[0] <= maxColumnWidth {
			narrow = append(narrow, b)
		}
	}
	if len(narrow) < 2 {
		return Columns{}
	}
	slices.SortFunc(narrow, func(a, b domain.BBox) int { return cmp.Compare(a[0], b[0]) })

	found := Columns{}
	widest := minGutter
	right := narrow[0][2]
	for _, b := range narrow[1:] {
		gap := b[0] - right
		mid := right + gap/2
		if gap > widest && mid >= gutterMin && mid <= gutterMax {
			widest = gap
			found = Columns{Split: mid, TwoColumn: true}
		}
		right = max(right, b[2])
	}
	return found
}

// Column は矩形が属する段を返す
//
// 1 が左段、2 が右段、0 は段を跨ぐ全幅要素
func (c Columns) Column(b domain.BBox) int {
	if !c.TwoColumn || len(b) != 4 {
		return 0
	}
	switch {
	case b[2] <= c.Split:
		return 1
	case b[0] >= c.Split:
		return 2
	default:
		return 0
	}
}

// orderPage は同一ページの要素を読み順に並べ、各要素に段番号を書き込む
//
// Textract の LAYOUT は読み順で返るとされるが、その保証を鵜呑みにせず座標から並べ直し、元の順と変わったかを第 2 戻り値で返す
// 全幅要素は段の流れを断ち切るため、そこで帯を分けて帯ごとに左段から右段へ読む
func orderPage(elems []Element) ([]Element, bool) {
	boxes := make([]domain.BBox, len(elems))
	for i := range elems {
		boxes[i] = elems[i].BBox
	}
	cols := DetectColumns(boxes)
	for i := range elems {
		elems[i].Column = cols.Column(elems[i].BBox)
	}
	if !cols.TwoColumn {
		return elems, false
	}

	idx := make([]int, len(elems))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		return cmp.Compare(bboxTop(elems[a].BBox), bboxTop(elems[b].BBox))
	})

	band := make([]int, len(elems))
	n := 0
	for _, i := range idx {
		if elems[i].Column == 0 {
			n++
			band[i] = n
			n++
			continue
		}
		band[i] = n
	}

	slices.SortStableFunc(idx, func(a, b int) int {
		if v := cmp.Compare(band[a], band[b]); v != 0 {
			return v
		}
		if v := cmp.Compare(elems[a].Column, elems[b].Column); v != 0 {
			return v
		}
		if v := cmp.Compare(bboxTop(elems[a].BBox), bboxTop(elems[b].BBox)); v != 0 {
			return v
		}
		return cmp.Compare(bboxLeft(elems[a].BBox), bboxLeft(elems[b].BBox))
	})

	out := make([]Element, len(elems))
	reordered := false
	for k, i := range idx {
		out[k] = elems[i]
		if k != i {
			reordered = true
		}
	}
	return out, reordered
}

func bboxLeft(b domain.BBox) float64 {
	if len(b) != 4 {
		return 0
	}
	return b[0]
}

func bboxTop(b domain.BBox) float64 {
	if len(b) != 4 {
		return 0
	}
	return b[1]
}

// overlapArea は 2 つの矩形が重なる面積を返す (LAYOUT_TABLE と TABLE の対応づけに用いる)
func overlapArea(a, b domain.BBox) float64 {
	if len(a) != 4 || len(b) != 4 {
		return 0
	}
	w := min(a[2], b[2]) - max(a[0], b[0])
	h := min(a[3], b[3]) - max(a[1], b[1])
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}
