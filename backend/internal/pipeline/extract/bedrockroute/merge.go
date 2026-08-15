package bedrockroute

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// MergeInput は結合に必要な、ページ結果の外側から与える情報
type MergeInput struct {
	JobID string // JobID は処理単位の識別子

	// Source はバリデーションと前処理で確定済みの入力 PDF の属性
	//
	// 抽出では決まらない情報であるため結合では触らずそのまま載せ、欠落ページの検出にだけ PageCount を用いる
	Source domain.Source

	Pages []PageResult // Pages はページ順不同でよい (ページ番号の昇順に整列してから結合する)
}

// Merge はページ単位の結果を 1 つの構造化 JSON へ結合する
//
// Map の一部が失敗してページが欠けても中断せず、欠落を provenance.warnings に残して続行する
// 片方の経路が部分的にしか揃わなくても比較の材料は得たいため、エラーにはしない
//
// ExtractedAt DurationMs Confidence はここでは埋めない — 計測と信頼度の算出は呼び出し側が担う
func Merge(in MergeInput) domain.Document {
	pages, warnings := orderPages(in.Pages, in.Source.PageCount)

	return domain.Document{
		JobID:         in.JobID,
		SchemaVersion: domain.SchemaVersion,
		Source:        in.Source,
		Metadata:      mergeMetadata(pages),
		Sections:      mergeSections(pages),
		Figures:       mergeFigures(pages),
		Tables:        mergeTables(pages),
		References:    mergeReferences(pages),
		Provenance: domain.Provenance{
			Route:    domain.RouteBedrock,
			Cost:     mergeCost(pages),
			Warnings: warnings,
		},
	}
}

// orderPages はページ結果をページ番号の昇順に整列し、結合の前提を満たさないものを警告に落とす
//
// Map の再実行で同じページが二重に届くことがあるため、重複は先着を残す
// pageCount が 0 の場合は観測した最大ページ番号までを期待値とし、末尾の欠落は検出できない
func orderPages(pages []PageResult, pageCount int) ([]PageResult, []string) {
	var warnings []string

	ordered := make([]PageResult, 0, len(pages))
	for _, p := range pages {
		if p.Page < 1 {
			warnings = append(warnings, fmt.Sprintf("ページ番号が不正な結果を除外した: page=%d", p.Page))
			continue
		}
		ordered = append(ordered, p)
	}
	slices.SortStableFunc(ordered, func(a, b PageResult) int { return cmp.Compare(a.Page, b.Page) })

	seen := make(map[int]bool, len(ordered))
	deduped := make([]PageResult, 0, len(ordered))
	for _, p := range ordered {
		if seen[p.Page] {
			warnings = append(warnings, fmt.Sprintf("同じページの結果が重複したため後着を除外した: page=%d", p.Page))
			continue
		}
		seen[p.Page] = true
		deduped = append(deduped, p)
	}

	expected := pageCount
	if expected <= 0 && len(deduped) > 0 {
		expected = deduped[len(deduped)-1].Page
	}
	for page := 1; page <= expected; page++ {
		if !seen[page] {
			warnings = append(warnings, fmt.Sprintf("ページの抽出結果が欠落したまま結合した: page=%d", page))
		}
	}

	return deduped, warnings
}

// mergeMetadata は書誌情報をページ順の先着で埋める
//
// 通常は表紙で揃うが、表紙のページが欠落した場合に後続ページの値を拾えるよう先着を採る
func mergeMetadata(pages []PageResult) domain.Metadata {
	var m domain.Metadata
	for _, p := range pages {
		if m.Title == "" {
			m.Title = strings.TrimSpace(p.Title)
		}
		if m.Abstract == "" {
			m.Abstract = strings.TrimSpace(p.Abstract)
		}
		if len(m.Authors) == 0 {
			m.Authors = p.Authors
		}
		if len(m.Keywords) == 0 {
			m.Keywords = p.Keywords
		}
	}
	return m
}

// mergeSections は章がページを跨ぐ場合を結合する
//
// 判定は continuesPreviousSection フラグと見出しの有無だけで機械的に行い、結合のためにモデルを呼び直さない
// 呼び直せばコストと非決定性がページ数に比例して増える
//
// 規則は次のとおり
//   - 見出しのあるブロックは常に新しい章を開始する
//   - ページ途中の見出しなしブロックは直前の章に連結する
//   - ページ冒頭の見出しなしブロックは、フラグが欠落または true なら直前の章に連結し、false なら見出しなしの章を開始する
func mergeSections(pages []PageResult) []domain.Section {
	sections := make([]domain.Section, 0)
	open := -1

	for _, p := range pages {
		for i, s := range p.Sections {
			heading := strings.TrimSpace(s.Heading)
			text := strings.TrimSpace(s.Text)
			continues := i > 0 || p.ContinuesPreviousSection == nil || *p.ContinuesPreviousSection

			if heading == "" && continues && open >= 0 {
				sections[open].Text = joinFragments(sections[open].Text, text)
				sections[open].Pages = appendPage(sections[open].Pages, p.Page)
				continue
			}

			sections = append(sections, domain.Section{
				Level:   s.Level,
				Heading: heading,
				Text:    text,
				Pages:   []int{p.Page},
			})
			open = len(sections) - 1
		}
	}
	return sections
}

// mergeFigures は図をページ順に並べ、キャプションとページ番号を保持する
func mergeFigures(pages []PageResult) []domain.Figure {
	figures := make([]domain.Figure, 0)
	for _, p := range pages {
		for _, f := range p.Figures {
			// BBox は経路 B では取得できないため空のままとし、omitempty で bbox キーごと落とす
			figures = append(figures, domain.Figure{
				ID:      strings.TrimSpace(f.Label),
				Caption: strings.TrimSpace(f.Caption),
				Page:    p.Page,
			})
		}
	}
	return figures
}

// mergeTables は表をページ順に並べる
func mergeTables(pages []PageResult) []domain.Table {
	tables := make([]domain.Table, 0)
	for _, p := range pages {
		for _, t := range p.Tables {
			tables = append(tables, domain.Table{
				ID:      strings.TrimSpace(t.Label),
				Caption: strings.TrimSpace(t.Caption),
				Page:    p.Page,
				Header:  nonNilRows(t.Header),
				Rows:    nonNilRows(t.Rows),
			})
		}
	}
	return tables
}

// nonNilRows は header と rows が null ではなく空配列として出力されるようにする
//
// 経路間で同じ形の差分を取るため、要素が無い場合もキーの型を配列に揃える
func nonNilRows(rows [][]string) [][]string {
	if rows == nil {
		return [][]string{}
	}
	return rows
}

// mergeReferences は参照文献を連結する
//
// ページ冒頭の 1 件が前ページ末尾の続きかは continuesPreviousReference で判定し、続きであれば raw を繋いで 1 件に畳む
func mergeReferences(pages []PageResult) []domain.Reference {
	refs := make([]domain.Reference, 0)
	for _, p := range pages {
		for i, r := range p.References {
			continues := i == 0 && p.ContinuesPreviousReference != nil && *p.ContinuesPreviousReference
			if continues && len(refs) > 0 {
				last := &refs[len(refs)-1]
				last.Raw = joinFragments(last.Raw, strings.TrimSpace(r.Raw))
				fillReference(last, r)
				continue
			}
			refs = append(refs, toReference(r))
		}
	}
	return refs
}

// toReference はページ上の 1 件を domain の型へ写す
func toReference(r PageReference) domain.Reference {
	ref := domain.Reference{
		Raw:     strings.TrimSpace(r.Raw),
		Title:   strings.TrimSpace(r.Title),
		Authors: r.Authors,
		Year:    r.Year,
		Venue:   strings.TrimSpace(r.Venue),
	}
	if doi := strings.TrimSpace(r.DOI); doi != "" {
		ref.DOI = &doi
	}
	return ref
}

// fillReference はページを跨いだ断片が持つ情報で、欠けている項目だけを埋める
//
// 前半に書誌情報が載り DOI だけが後半へ回る並びがあるため、断片側も取りこぼさない
func fillReference(dst *domain.Reference, src PageReference) {
	if dst.Title == "" {
		dst.Title = strings.TrimSpace(src.Title)
	}
	if len(dst.Authors) == 0 {
		dst.Authors = src.Authors
	}
	if dst.Year == 0 {
		dst.Year = src.Year
	}
	if dst.Venue == "" {
		dst.Venue = strings.TrimSpace(src.Venue)
	}
	if dst.DOI == nil {
		if doi := strings.TrimSpace(src.DOI); doi != "" {
			dst.DOI = &doi
		}
	}
}

// mergeCost はページごとのトークン数を合算する
//
// TextractPages と TextractFeatures は経路 B では発生しないため空のままとする
func mergeCost(pages []PageResult) domain.Cost {
	var cost domain.Cost
	for _, p := range pages {
		if cost.BedrockModel == "" {
			cost.BedrockModel = p.ModelID
		}
		cost.BedrockInputTokens += int(p.Usage.InputTokens)
		cost.BedrockOutputTokens += int(p.Usage.OutputTokens)
	}
	return cost
}

// appendPage はページ番号を昇順・重複なしで足す
func appendPage(pages []int, page int) []int {
	if len(pages) > 0 && pages[len(pages)-1] == page {
		return pages
	}
	return append(pages, page)
}

// joinFragments はページを跨いで分断された文字列を連結する
//
// 常に空白で繋ぐと日本語の文中に原本にない空白が入るため、境界の文字がどちらも ASCII のときだけ空白を挟む
func joinFragments(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}

	last, _ := utf8.DecodeLastRuneInString(a)
	first, _ := utf8.DecodeRuneInString(b)
	if last < utf8.RuneSelf && first < utf8.RuneSelf {
		return a + " " + b
	}
	return a + b
}
