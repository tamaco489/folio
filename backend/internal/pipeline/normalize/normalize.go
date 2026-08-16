// Package normalize は両経路の抽出結果を比較可能な中間表現へ揃える
//
// 経路 A (textractroute) と経路 B (bedrockroute) はどちらも domain.Document を返すため、経路固有の形式を変換するのではなく、同じ型の中の表現のゆれだけを揃える
// 揃えるのは空白、番号付き見出しのレベル、DOI の表記、年の妥当性、欠落したスライスの形といった機械的なゆれに限る
// 抽出内容の差 (bbox の有無、confidence の有無、表の崩れ、読み順、著者名の表記) は比較対象そのものであるため残す
//
// extractedAt と durationMs の値を知っているのは Lambda 側であり、この層は値を捏造せず検証だけを行う
package normalize

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// warningPrefix は正規化で判断できなかったことを経路の警告と区別する接頭辞
const warningPrefix = "normalize: "

// Option は Normalize の設定を差し替える
type Option func(*normalizer)

// WithClock は年の妥当性判定に用いる現在時刻の取得を差し替える (テストで判定を決定的にするために用いる)
func WithClock(now func() time.Time) Option {
	return func(n *normalizer) { n.now = now }
}

type normalizer struct {
	now      func() time.Time
	warnings []string
}

func (n *normalizer) warnf(format string, args ...any) {
	n.warnings = append(n.warnings, warningPrefix+fmt.Sprintf(format, args...))
}

// Normalize は経路の出力を比較可能な中間表現へ揃える
//
// 入力は変更せず、正規化した複製を返す
// 同じ入力に対して冪等であり、エラーは返さない
// 正規化で判断できなかったことは provenance.warnings に "normalize: " の接頭辞で追記する
func Normalize(doc domain.Document, opts ...Option) domain.Document {
	n := &normalizer{now: time.Now}
	for _, opt := range opts {
		opt(n)
	}

	out := domain.Document{
		JobID:         doc.JobID,
		SchemaVersion: doc.SchemaVersion,
		Source:        doc.Source,
		Metadata:      n.metadata(doc.Metadata),
		Sections:      n.sections(doc.Sections),
		Figures:       figures(doc.Figures),
		Tables:        tables(doc.Tables),
		References:    n.references(doc.References),
	}
	if out.SchemaVersion == "" {
		out.SchemaVersion = domain.SchemaVersion
	}
	if out.JobID == "" {
		n.warnf("jobId が空である")
	}

	// 警告を集め終えてから provenance を組み立てる
	out.Provenance = n.provenance(doc.Provenance)
	return out
}

func (n *normalizer) metadata(m domain.Metadata) domain.Metadata {
	authors := make([]domain.Author, 0, len(m.Authors))
	for _, a := range m.Authors {
		authors = append(authors, domain.Author{
			Name:        collapseSpace(a.Name),
			Affiliation: collapseSpace(a.Affiliation),
			Email:       collapseSpace(a.Email),
		})
	}
	return domain.Metadata{
		Title:           collapseSpace(m.Title),
		Authors:         authors,
		Abstract:        collapseSpace(m.Abstract),
		Keywords:        collapseStrings(m.Keywords),
		Venue:           collapseSpace(m.Venue),
		Year:            n.year(m.Year, "metadata"),
		PrimaryCategory: collapseSpace(m.PrimaryCategory),
	}
}

func (n *normalizer) sections(ss []domain.Section) []domain.Section {
	out := make([]domain.Section, 0, len(ss))
	for i, s := range ss {
		heading := collapseSpace(s.Heading)
		level := headingLevel(heading, s.Level)
		switch {
		case level < 1:
			n.warnf("sections[%d] の見出しレベル %d を 1 に丸めた", i, level)
			level = 1
		case level > maxHeadingLevel:
			n.warnf("sections[%d] の見出しレベル %d を上限の %d に丸めた", i, level, maxHeadingLevel)
			level = maxHeadingLevel
		}
		out = append(out, domain.Section{
			Level:   level,
			Heading: heading,
			Text:    normalizeText(s.Text),
			Pages:   sortedPages(s.Pages),
		})
	}
	return out
}

// sortedPages はページ番号を昇順・重複なしに揃える (順序に情報が無いため)
func sortedPages(pages []int) []int {
	out := append(make([]int, 0, len(pages)), pages...)
	slices.Sort(out)
	return slices.Compact(out)
}

func figures(fs []domain.Figure) []domain.Figure {
	out := make([]domain.Figure, 0, len(fs))
	for _, f := range fs {
		out = append(out, domain.Figure{
			ID:      collapseSpace(f.ID),
			Caption: collapseSpace(f.Caption),
			Page:    f.Page,
			BBox:    slices.Clone(f.BBox),
		})
	}
	return out
}

func tables(ts []domain.Table) []domain.Table {
	out := make([]domain.Table, 0, len(ts))
	for _, t := range ts {
		out = append(out, domain.Table{
			ID:      collapseSpace(t.ID),
			Caption: collapseSpace(t.Caption),
			Page:    t.Page,
			BBox:    slices.Clone(t.BBox),
			Header:  cells(t.Header),
			Rows:    cells(t.Rows),
		})
	}
	return out
}

// cells は表のセルの前後の空白だけを除く
//
// セル内の空白や行列の形は崩れも含めて比較対象であるため揃えない
func cells(rows [][]string) [][]string {
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		cols := make([]string, 0, len(row))
		for _, c := range row {
			cols = append(cols, strings.TrimSpace(c))
		}
		out = append(out, cols)
	}
	return out
}

// provenance は由来を検証し、集合として扱う項目の並びを揃える
//
// route、extractedAt、durationMs の値は変えず、不審な値は警告に残すだけとする
func (n *normalizer) provenance(p domain.Provenance) domain.Provenance {
	if p.Route != domain.RouteTextract && p.Route != domain.RouteBedrock {
		n.warnf("provenance.route が未知の経路である: %q", p.Route)
	}
	if p.ExtractedAt.IsZero() {
		n.warnf("provenance.extractedAt が未設定である")
	}
	if p.DurationMs < 0 {
		n.warnf("provenance.durationMs が負である: %d", p.DurationMs)
	}

	out := p
	out.Confidence = domain.Confidence{
		Title:      clonePtr(p.Confidence.Title),
		Authors:    clonePtr(p.Confidence.Authors),
		Abstract:   clonePtr(p.Confidence.Abstract),
		Sections:   clonePtr(p.Confidence.Sections),
		Figures:    clonePtr(p.Confidence.Figures),
		Tables:     clonePtr(p.Confidence.Tables),
		References: clonePtr(p.Confidence.References),
	}
	// TextractFeatures は集合であり順序に情報が無い
	features := slices.Clone(p.Cost.TextractFeatures)
	slices.Sort(features)
	out.Cost.TextractFeatures = slices.Compact(features)
	// 経路の結合で同じ警告が繰り返されうるため重複を除く (自身の警告も含めて除くことで 2 回掛けても増えない)
	out.Warnings = dedupe(append(slices.Clone(p.Warnings), n.warnings...))
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	return domain.Ptr(*p)
}
