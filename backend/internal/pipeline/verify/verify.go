// Package verify は正規化した抽出結果を原本と外部データに照らし、フィールド単位の信頼度とレビュー要否を決める
//
// モデルの自己申告より、原本 (テキストレイヤー) や外部 (Crossref) との突合の方が信頼できるという方針の層である
// 根拠は 3 種で、無いものは 0 ではなく除外して残りを平均する (経路 A と経路 B に同じ式を使いつつ、Textract の確信度を持たない経路 B が不当に低くならないようにするため)
//   - 原本との一致度: 抽出値がテキストレイヤーに存在するか
//   - Textract の確信度: 経路 A の入力に載っている値
//   - Crossref の照合結果: 参照文献のみ
//
// S3 は読み書きしない。原本テキストは呼び出し側が work/{jobId}/text/layer.txt を読んで Original として渡す
// エラーは返さない。Crossref が落ちても原本が無くても結果を返し、根拠が無いフィールドは nil のままにする
package verify

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// Original は原本のテキストレイヤー
type Original struct {
	Text         string // Text は work/{jobId}/text/layer.txt の内容
	HasTextLayer bool   // HasTextLayer は前処理の判定 (false なら Text があっても突合しない)
}

// Option は Verifier の設定を差し替える
type Option func(*Verifier)

// WithClock は Report.VerifiedAt に用いる現在時刻の取得を差し替える
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) { v.now = now }
}

// WithReviewThreshold はレビュー要否の閾値を差し替える
func WithReviewThreshold(t float64) Option {
	return func(v *Verifier) { v.threshold = t }
}

// Verifier は突合と外部照合を束ねる
type Verifier struct {
	resolver  Resolver
	now       func() time.Time
	threshold float64
}

// New は Verifier を組み立てる
//
// resolver が nil なら外部照合を行わず、参照文献はすべて skipped になる
func New(resolver Resolver, opts ...Option) *Verifier {
	v := &Verifier{
		resolver:  resolver,
		now:       time.Now,
		threshold: DefaultReviewThreshold,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Verify は文書を原本と Crossref に照らし、信頼度を埋めた複製と根拠を返す
//
// 入力は変更しない。Provenance の Confidence と Warnings は複製し、それ以外のスライスは入力と共有する
// 警告は "verify: " の接頭辞で Provenance.Warnings に追記する
// ctx のキャンセルは Crossref の照合を打ち切るだけで、結果は返す
func (v *Verifier) Verify(ctx context.Context, doc domain.Document, orig Original) (domain.Document, Report) {
	report := Report{
		JobID:           doc.JobID,
		Route:           doc.Provenance.Route,
		VerifiedAt:      v.now().UTC(),
		Hallucinations:  []Hallucination{},
		ReviewThreshold: v.threshold,
	}
	var warnings []string
	warnf := func(format string, args ...any) {
		warnings = append(warnings, warningPrefix+fmt.Sprintf(format, args...))
	}

	// テキストレイヤーが無ければ突合を省略し、他の根拠だけで信頼度を出す (経路 B 同士の突き合わせはしない。1 本しか無いため)
	m := &matcher{}
	if orig.HasTextLayer && fold(orig.Text) != "" {
		m.corpus = newCorpus(orig.Text)
		report.OriginalChecked = true
	} else {
		warnf("テキストレイヤーが無いため原本との突合を省略した")
	}
	original := m.match(doc)
	if n := len(m.hallucinations); n > 0 {
		warnf("原本に見つからない抽出値が %d 件ある", n)
	}
	report.Hallucinations = append(report.Hallucinations, m.hallucinations...)

	report.References = v.checkReferences(ctx, doc.References)
	report.Crossref, report.Fields.References.Crossref = summarize(report.References)
	if v.resolver == nil {
		warnf("Crossref の照合を行わなかった (Resolver 未設定)")
	} else if over := len(doc.References) - MaxLookups; over > 0 {
		warnf("参照文献が上限 %d 件を超えたため %d 件の外部照合を省略した", MaxLookups, over)
	}
	if s := report.Crossref; s.NotFound > 0 || s.Unavailable > 0 {
		warnf("Crossref で照合できなかった参照文献がある: notFound=%d unavailable=%d", s.NotFound, s.Unavailable)
	}
	if s := report.Crossref; s.Mismatch > 0 {
		warnf("DOI が別の文献を指す参照文献が %d 件ある", s.Mismatch)
	}
	for _, c := range report.References {
		if c.Status == StatusMismatch {
			report.Hallucinations = append(report.Hallucinations, Hallucination{
				Path:  fmt.Sprintf("references[%d].doi", c.Index),
				Value: c.DOI,
				Score: *c.Similarity,
			})
		}
	}

	// Textract の確信度は経路 A の入力にだけ載る
	var textract domain.Confidence
	if doc.Provenance.Route == domain.RouteTextract {
		textract = doc.Provenance.Confidence
	}
	fields := &report.Fields
	for _, f := range []struct {
		dst      *FieldReport
		original signal
		textract *float64
	}{
		{&fields.Title, original.title, textract.Title},
		{&fields.Authors, original.authors, textract.Authors},
		{&fields.Abstract, original.abstract, textract.Abstract},
		{&fields.Sections, original.sections, textract.Sections},
		{&fields.Figures, original.figures, textract.Figures},
		{&fields.Tables, original.tables, textract.Tables},
		{&fields.References, original.references, textract.References},
	} {
		f.dst.Original, f.dst.Items = f.original.mean(), f.original.n
		f.dst.Textract = clonePtr(f.textract)
		f.dst.Confidence = combine(f.dst.Original, f.dst.Textract, f.dst.Crossref)
	}

	report.NeedsReview = !report.OriginalChecked || len(report.Hallucinations) > 0
	for _, f := range []FieldReport{fields.Title, fields.Authors, fields.Abstract, fields.Sections, fields.Figures, fields.Tables, fields.References} {
		if f.Confidence != nil && *f.Confidence < v.threshold {
			report.NeedsReview = true
		}
	}

	out := doc
	out.Provenance.Confidence = domain.Confidence{
		Title:      clonePtr(fields.Title.Confidence),
		Authors:    clonePtr(fields.Authors.Confidence),
		Abstract:   clonePtr(fields.Abstract.Confidence),
		Sections:   clonePtr(fields.Sections.Confidence),
		Figures:    clonePtr(fields.Figures.Confidence),
		Tables:     clonePtr(fields.Tables.Confidence),
		References: clonePtr(fields.References.Confidence),
	}
	out.Provenance.Warnings = append(slices.Clone(doc.Provenance.Warnings), warnings...)
	return out, report
}

// signal は 1 フィールドに集まった一致度の集計
type signal struct {
	sum float64
	n   int
}

func (s *signal) add(score float64) {
	s.sum += score
	s.n++
}

// mean は集計した一致度の平均を返す (項目が無ければ nil)
func (s signal) mean() *float64 {
	if s.n == 0 {
		return nil
	}
	return new(round(s.sum / float64(s.n)))
}

// originalScores はフィールドごとの原本との一致度
type originalScores struct {
	title, authors, abstract, sections, figures, tables, references signal
}

// matcher は原本との突合を担う (corpus が nil なら何も突合しない)
type matcher struct {
	corpus         *corpus
	hallucinations []Hallucination
}

// short は短い値を突合し、一致度が低ければハルシネーションとして記録する
func (m *matcher) short(s *signal, path, value string) {
	if m.corpus == nil {
		return
	}
	score, ok := m.corpus.shortScore(value)
	if !ok {
		return
	}
	s.add(score)
	if score < hallucinationThreshold {
		m.hallucinations = append(m.hallucinations, Hallucination{Path: path, Value: value, Score: round(score)})
	}
}

// long は長い値 (要旨、本文) をトークン対の被覆率で突合する
//
// 読み順の違いや段落の再構成で部分文字列一致は壊れるため、短い値と同じ扱いにしない
// ハルシネーションとしては記録せず、一致度の低さがそのまま信頼度に現れる
func (m *matcher) long(s *signal, value string) {
	if m.corpus == nil {
		return
	}
	if score, ok := m.corpus.coverage(value); ok {
		s.add(score)
	}
}

// match は突合の対象とするフィールドを走査する
func (m *matcher) match(doc domain.Document) originalScores {
	var o originalScores
	m.short(&o.title, "metadata.title", doc.Metadata.Title)
	for i, a := range doc.Metadata.Authors {
		m.short(&o.authors, fmt.Sprintf("metadata.authors[%d].name", i), a.Name)
	}
	m.long(&o.abstract, doc.Metadata.Abstract)
	for i, s := range doc.Sections {
		m.short(&o.sections, fmt.Sprintf("sections[%d].heading", i), s.Heading)
		m.long(&o.sections, s.Text)
	}
	for i, f := range doc.Figures {
		m.short(&o.figures, fmt.Sprintf("figures[%d].caption", i), f.Caption)
	}
	for i, t := range doc.Tables {
		m.short(&o.tables, fmt.Sprintf("tables[%d].caption", i), t.Caption)
		m.cells(&o.tables, t)
	}
	for i, r := range doc.References {
		// Raw が原本の記載そのものであり、無ければ題名で代える
		if r.Raw != "" {
			m.short(&o.references, fmt.Sprintf("references[%d].raw", i), r.Raw)
		} else {
			m.short(&o.references, fmt.Sprintf("references[%d].title", i), r.Title)
		}
	}
	return o
}

// cells は表のセル全体を 1 項目として突合する
//
// セルは短く数値が多いため、部分文字列一致は他の数値や DOI の一部に紛れて当たりやすい
// トークン対の被覆率 ("92.6" は 92 と 6 の対) をセルごとに取り、その平均を表 1 つにつき 1 項目として足す (セル数でキャプションを埋もれさせないため)
func (m *matcher) cells(s *signal, t domain.Table) {
	if m.corpus == nil {
		return
	}
	var cells signal
	for _, row := range slices.Concat(t.Header, t.Rows) {
		for _, cell := range row {
			if score, ok := m.corpus.coverage(cell); ok {
				cells.add(score)
			}
		}
	}
	if cells.n > 0 {
		s.add(cells.sum / float64(cells.n))
	}
}

// summarize は照合結果を件数に畳み、Crossref のシグナルを返す
//
// シグナルは照合できた件 (verified + mismatch) のうち一致した割合とする
// notFound は未確認であって根拠にならないため分母に入れない (arXiv の DOI や Crossref 未登録の文献が多い文書を不当に下げないため)
func summarize(checks []ReferenceCheck) (CrossrefSummary, *float64) {
	var s CrossrefSummary
	for _, c := range checks {
		switch c.Status {
		case StatusVerified:
			s.Verified++
		case StatusMismatch:
			s.Mismatch++
		case StatusNotFound:
			s.NotFound++
		case StatusUnavailable:
			s.Unavailable++
		case StatusSkipped:
			s.Skipped++
		}
	}
	if s.Verified+s.Mismatch == 0 {
		return s, nil
	}
	return s, new(round(float64(s.Verified) / float64(s.Verified+s.Mismatch)))
}

// combine は存在するシグナルの平均を返す (1 つも無ければ nil)
func combine(signals ...*float64) *float64 {
	var s signal
	for _, p := range signals {
		if p != nil {
			s.add(*p)
		}
	}
	return s.mean()
}

// round は信頼度を小数第 4 位に丸める (レビュー閾値との比較は丸めた値で行う)
func round(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func clonePtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	return new(*p)
}
