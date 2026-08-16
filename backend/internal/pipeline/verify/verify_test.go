package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify/crossref"
)

var _ Resolver = (*crossref.Client)(nil)

// syntheticLayer は pdftotext の出力を模した合成の原本テキスト (実 PDF 由来ではない)
//
// 行の折り返し、行末ハイフネーション、合字、上付き文字、改ページを含む
const syntheticLayer = "Sparse Attention Routing for\nLong-Context Language Models\n\n" +
	"Aiko Tanaka1 Marcus Feldman2\n1Graduate School of Information Science, Tohoku University\n2Department of Computer Science, ETH Zurich\n\n" +
	"Abstract\nLong-context language models suﬀer from quadratic atten-\ntion cost. We propose a routing mechanism that selects a\nsparse subset of key-value pairs per query head.\n\n" +
	"1 Introduction\nTransformer-based language models have scaled context win-\ndows from 2K to over 1M tokens. The attention mechanism\nremains quadratic in sequence length.\n\f" +
	"3.1 Routing Function\nGiven a query vector q and a set of key vectors K, the router\ncomputes a relevance score.\n\n" +
	"Figure 1: Overview of the sparse routing architecture.\n\n" +
	"Table 2: Results on long-context retrieval benchmarks.\nModel NIAH LongBench Speedup\n1K 128K Avg\nDense baseline 100.0 94.2 48.7 1.0x\nOurs 100.0 92.6 47.8 6.2x\n\n" +
	"References\n[1] Y. LeCun, Y. Bengio, and G. Hinton. Deep learning. Nature,\n521:436–444, 2015.\n" +
	"[2] G. Xiao, Y. Tian, B. Chen, S. Han, and M. Lewis. Eﬃcient\nstreaming language models with attention sinks. In ICLR, 2024.\n"

const (
	rawLeCun = "LeCun, Y., Bengio, Y., Hinton, G. Deep learning. Nature 521, 436–444 (2015)."
	rawXiao  = "Xiao, G., Tian, Y., Chen, B., et al. Efficient Streaming Language Models with Attention Sinks. ICLR, 2024."
)

func fixedClock() time.Time {
	return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
}

// sampleDocument は normalize を通した後の想定で、原本に無い値 (著者、図、参照文献) と Crossref の 4 系統を含む文書を返す
func sampleDocument(route domain.Route) domain.Document {
	doc := domain.Document{
		JobID:         "01JB8X7K2M9QRT4V6WZ3N5PDA0",
		SchemaVersion: domain.SchemaVersion,
		Source:        domain.Source{Language: domain.LanguageEnglish, PageCount: 2, HasTextLayer: true},
		Metadata: domain.Metadata{
			Title: "Sparse Attention Routing for Long-Context Language Models",
			Authors: []domain.Author{
				{Name: "Aiko Tanaka"},
				{Name: "Marcus Feldman"},
				{Name: "John Doe"},
			},
			Abstract: "Long-context language models suffer from quadratic attention cost. We propose a routing mechanism that selects a sparse subset of key-value pairs per query head.",
		},
		Sections: []domain.Section{
			{Level: 1, Heading: "1 Introduction", Text: "Transformer-based language models have scaled context windows from 2K to over 1M tokens.\n\nThe attention mechanism remains quadratic in sequence length.", Pages: []int{1}},
			{Level: 2, Heading: "3.1 Routing Function", Text: "Given a query vector q and a set of key vectors K, the router computes a relevance score.", Pages: []int{2}},
			{Level: 1, Heading: "References", Text: "", Pages: []int{2}},
		},
		Figures: []domain.Figure{
			{ID: "figure-1", Caption: "Figure 1: Overview of the sparse routing architecture.", Page: 2},
			{ID: "figure-2", Caption: "Figure 2: Ablation over routing temperature.", Page: 2},
		},
		Tables: []domain.Table{{
			ID:      "table-1",
			Caption: "Table 2: Results on long-context retrieval benchmarks.",
			Page:    2,
			Header:  [][]string{{"Model", "NIAH", "NIAH", "LongBench", "Speedup"}, {"", "1K", "128K", "Avg", ""}},
			Rows:    [][]string{{"Dense baseline", "100.0", "94.2", "48.7", "1.0x"}, {"Ours", "100.0", "92.6", "47.8", "6.2x"}},
		}},
		References: []domain.Reference{
			{Raw: rawLeCun, Title: "Deep learning", DOI: domain.Ptr("10.1038/nature14539")},
			{Raw: rawXiao, Title: "Efficient Streaming Language Models with Attention Sinks", DOI: domain.Ptr("10.48550/arxiv.2309.17453")},
			{Raw: "Vaswani, A., Shazeer, N., Parmar, N., et al. Attention Is All You Need. NeurIPS, 2017.", Title: "Attention Is All You Need", DOI: domain.Ptr("10.1038/nature14539")},
			{Raw: rawLeCun},
			{Title: "Sparse Attention Routing for Long-Context Language Models"},
			{Raw: "Doe, J. Unavailable work. 2020.", DOI: domain.Ptr("10.1000/verify-unavailable")},
		},
		Provenance: domain.Provenance{
			Route:       route,
			ExtractedAt: fixedClock().Add(-time.Hour),
			Warnings:    []string{"page 2: 二段組と判定し読み順を並べ直した"},
		},
	}
	if route == domain.RouteTextract {
		doc.Provenance.Confidence = domain.Confidence{
			Title:    domain.Ptr(0.998),
			Sections: domain.Ptr(0.943),
			Figures:  domain.Ptr(0.912),
			Tables:   domain.Ptr(0.724),
		}
	}
	return doc
}

func sampleOriginal() Original {
	return Original{Text: syntheticLayer, HasTextLayer: true}
}

// replayResolver は記録済みの Crossref 応答を再生する Resolver (実時間を待たない)
func replayResolver(t *testing.T) *crossref.Client {
	t.Helper()
	replayer, err := crossref.NewReplayer(filepath.Join("testdata", "crossref"))
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	return crossref.New(
		crossref.WithTransport(replayer),
		crossref.WithSleeper(func(context.Context, time.Duration) error { return nil }),
	)
}

// fakeResolver は Crossref を模す (呼び出し回数を数える)
type fakeResolver struct {
	calls  int
	lookup func(doi string) (crossref.Work, error)
	search func(q string) ([]crossref.Work, error)
}

func (f *fakeResolver) LookupDOI(_ context.Context, doi string) (crossref.Work, error) {
	f.calls++
	if f.lookup == nil {
		return crossref.Work{}, errors.New("fake: no lookup")
	}
	return f.lookup(doi)
}

func (f *fakeResolver) Search(_ context.Context, q string) ([]crossref.Work, error) {
	f.calls++
	if f.search == nil {
		return nil, errors.New("fake: no search")
	}
	return f.search(q)
}

func ptrEq(p *float64, want float64) bool {
	return p != nil && *p == want
}

func fmtPtr(p *float64) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

func hasWarning(doc domain.Document, substr string) bool {
	for _, w := range doc.Provenance.Warnings {
		if strings.HasPrefix(w, warningPrefix) && strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func statuses(report Report) []ReferenceStatus {
	out := make([]ReferenceStatus, 0, len(report.References))
	for _, c := range report.References {
		out = append(out, c.Status)
	}
	return out
}

func hallucinationPaths(report Report) []string {
	out := make([]string, 0, len(report.Hallucinations))
	for _, h := range report.Hallucinations {
		out = append(out, h.Path)
	}
	return out
}

// 原本との突合と Crossref の再生を通した 1 本の文書で、根拠・ハルシネーション・レビュー要否がすべて揃うことを確かめる (経路 B)
func TestVerifyEndToEnd(t *testing.T) {
	v := New(replayResolver(t), WithClock(fixedClock))
	in := sampleDocument(domain.RouteBedrock)
	out, report := v.Verify(context.Background(), in, sampleOriginal())

	if !report.OriginalChecked || report.JobID != in.JobID || report.Route != domain.RouteBedrock || !report.VerifiedAt.Equal(fixedClock()) {
		t.Errorf("report のヘッダ = %+v", report)
	}

	// 原本との突合: 上付き文字、ハイフネーション、合字、行の折り返しを越えて一致し、原本に無い値だけが落ちること
	f := report.Fields
	if !ptrEq(f.Title.Original, 1) || !ptrEq(f.Abstract.Original, 1) || !ptrEq(f.Sections.Original, 1) {
		t.Errorf("title/abstract/sections の一致度 = %s %s %s, want 1 1 1", fmtPtr(f.Title.Original), fmtPtr(f.Abstract.Original), fmtPtr(f.Sections.Original))
	}
	if !ptrEq(f.Authors.Original, 0.6667) || f.Authors.Items != 3 {
		t.Errorf("authors = %s (items=%d), want 0.6667 (3)", fmtPtr(f.Authors.Original), f.Authors.Items)
	}
	if !ptrEq(f.Figures.Original, 0.5) || f.Figures.Items != 2 {
		t.Errorf("figures = %s (items=%d), want 0.5 (2)", fmtPtr(f.Figures.Original), f.Figures.Items)
	}
	if !ptrEq(f.Tables.Original, 1) || f.Tables.Items != 2 {
		t.Errorf("tables = %s (items=%d), want 1 (2: キャプション + セル)", fmtPtr(f.Tables.Original), f.Tables.Items)
	}
	if f.Sections.Items != 5 {
		t.Errorf("sections の項目数 = %d, want 5 (見出し 3 + 空でない本文 2)", f.Sections.Items)
	}

	// Crossref: verified / notFound (arXiv) / mismatch (DOI が別の文献) / verified (題名検索) / notFound (検索に一致なし) / unavailable (503)
	want := []ReferenceStatus{StatusVerified, StatusNotFound, StatusMismatch, StatusVerified, StatusNotFound, StatusUnavailable}
	if got := statuses(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if c := report.References[0]; c.MatchedDOI != "10.1038/nature14539" || c.MatchedTitle != "Deep learning" || !ptrEq(c.Similarity, 1) {
		t.Errorf("references[0] = %+v", c)
	}
	if c := report.References[2]; c.MatchedTitle != "Deep learning" || !ptrEq(c.Similarity, 0) {
		t.Errorf("references[2] = %+v", c)
	}
	if c := report.References[3]; c.Query != rawLeCun || c.MatchedDOI != "10.1038/nature14539" || !ptrEq(c.Similarity, 1) {
		t.Errorf("references[3] = %+v", c)
	}
	if c := report.References[4]; c.MatchedDOI == "" || c.Similarity == nil || *c.Similarity >= titleMatchThreshold {
		t.Errorf("references[4] は最も近い候補を残しつつ notFound になること: %+v", c)
	}
	if c := report.References[5]; !strings.Contains(c.Detail, "503") {
		t.Errorf("references[5].Detail = %q, want to mention 503", c.Detail)
	}
	if report.Crossref != (CrossrefSummary{Verified: 2, Mismatch: 1, NotFound: 2, Unavailable: 1}) {
		t.Errorf("crossref summary = %+v", report.Crossref)
	}
	// Crossref のシグナルは照合できた件 (verified + mismatch) の一致率であり、notFound と unavailable は分母に入れない
	if !ptrEq(f.References.Crossref, 0.6667) {
		t.Errorf("references.crossref = %s, want 0.6667", fmtPtr(f.References.Crossref))
	}

	// ハルシネーション: 原本に無い短い値と、別の文献を指す DOI
	wantPaths := []string{"metadata.authors[2].name", "figures[1].caption", "references[2].raw", "references[5].raw", "references[2].doi"}
	if got := hallucinationPaths(report); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("hallucinations = %v, want %v", got, wantPaths)
	}
	if h := report.Hallucinations[0]; h.Value != "John Doe" || h.Score != 0 {
		t.Errorf("hallucinations[0] = %+v", h)
	}
	if h := report.Hallucinations[4]; h.Value != "10.1038/nature14539" {
		t.Errorf("DOI の不一致は DOI を値として残すこと: %+v", h)
	}

	// 経路 B に Textract の確信度は無く、信頼度は原本 (と Crossref) だけから決まること
	if f.Title.Textract != nil || !ptrEq(f.Title.Confidence, 1) || !ptrEq(f.Figures.Confidence, 0.5) {
		t.Errorf("経路 B の title/figures = %+v %+v", f.Title, f.Figures)
	}
	if !ptrEq(out.Provenance.Confidence.Title, 1) || !ptrEq(out.Provenance.Confidence.Authors, 0.6667) || !ptrEq(out.Provenance.Confidence.Figures, 0.5) {
		t.Errorf("out.Provenance.Confidence = %+v", out.Provenance.Confidence)
	}
	if !reflect.DeepEqual(out.Provenance.Confidence.References, f.References.Confidence) || f.References.Confidence == nil {
		t.Errorf("references の信頼度が report と out で食い違う: %s vs %s", fmtPtr(out.Provenance.Confidence.References), fmtPtr(f.References.Confidence))
	}
	if !report.NeedsReview || report.ReviewThreshold != DefaultReviewThreshold {
		t.Errorf("needsReview = %v (threshold %v), want true", report.NeedsReview, report.ReviewThreshold)
	}

	// 警告: 経路の警告を残し、verify の警告を接頭辞付きで足す
	if out.Provenance.Warnings[0] != in.Provenance.Warnings[0] {
		t.Errorf("経路の警告が失われた: %q", out.Provenance.Warnings)
	}
	for _, want := range []string{"原本に見つからない抽出値が 4 件", "notFound=2 unavailable=1", "DOI が別の文献を指す参照文献が 1 件"} {
		if !hasWarning(out, want) {
			t.Errorf("警告 %q が無い: %q", want, out.Provenance.Warnings)
		}
	}
	if hasWarning(out, "テキストレイヤー") || hasWarning(out, "上限") {
		t.Errorf("出るべきでない警告がある: %q", out.Provenance.Warnings)
	}
}

// 経路 A では Textract の確信度をシグナルとして平均に入れ、無いフィールドは原本だけで決めること
func TestVerifyRouteTextractCombinesSignals(t *testing.T) {
	v := New(replayResolver(t), WithClock(fixedClock))
	out, report := v.Verify(context.Background(), sampleDocument(domain.RouteTextract), sampleOriginal())

	f := report.Fields
	if !ptrEq(f.Title.Textract, 0.998) || !ptrEq(f.Title.Confidence, 0.999) {
		t.Errorf("title = %+v, want textract 0.998, confidence 0.999", f.Title)
	}
	if !ptrEq(f.Figures.Confidence, 0.706) {
		t.Errorf("figures = %+v, want (0.5 + 0.912) / 2 = 0.706", f.Figures)
	}
	if !ptrEq(f.Sections.Confidence, 0.9715) {
		t.Errorf("sections = %+v, want (1 + 0.943) / 2", f.Sections)
	}
	// Textract の確信度が無いフィールドは原本だけで決まり、経路 B と同じ値になること
	if f.Authors.Textract != nil || !ptrEq(f.Authors.Confidence, 0.6667) {
		t.Errorf("authors = %+v", f.Authors)
	}
	if !ptrEq(out.Provenance.Confidence.Title, 0.999) {
		t.Errorf("out.Provenance.Confidence.Title = %s", fmtPtr(out.Provenance.Confidence.Title))
	}
}

// テキストレイヤーが無い場合は突合を省略し、残る根拠だけで信頼度を出し、レビュー要に倒すこと
func TestVerifyWithoutTextLayer(t *testing.T) {
	tests := map[string]struct {
		route domain.Route
		orig  Original
	}{
		"正常系_経路 A で hasTextLayer が false の場合_Textract の確信度だけで決まること": {route: domain.RouteTextract, orig: Original{Text: syntheticLayer, HasTextLayer: false}},
		"正常系_経路 B で本文が実質空の場合_参照文献の Crossref 以外は nil のままであること":       {route: domain.RouteBedrock, orig: Original{Text: " \n\f ", HasTextLayer: true}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			v := New(replayResolver(t), WithClock(fixedClock))
			out, report := v.Verify(context.Background(), sampleDocument(tt.route), tt.orig)

			if report.OriginalChecked {
				t.Fatal("突合が省略されていない")
			}
			if !hasWarning(out, "テキストレイヤーが無いため") {
				t.Errorf("警告が無い: %q", out.Provenance.Warnings)
			}
			if !report.NeedsReview {
				t.Error("needsReview = false, want true")
			}
			for _, h := range report.Hallucinations {
				if !strings.HasSuffix(h.Path, ".doi") {
					t.Errorf("突合を省略したのに原本由来のハルシネーションがある: %+v", h)
				}
			}
			c := out.Provenance.Confidence
			if c.Authors != nil || c.Abstract != nil || report.Fields.Title.Original != nil || report.Fields.Title.Items != 0 {
				t.Errorf("突合を省略したのに原本の根拠がある: %+v / %+v", c, report.Fields.Title)
			}
			// 参照文献は Crossref だけで決まる
			if !ptrEq(c.References, 0.6667) {
				t.Errorf("references = %s, want 0.6667 (Crossref のみ)", fmtPtr(c.References))
			}
			if tt.route == domain.RouteTextract {
				if !ptrEq(c.Title, 0.998) || !ptrEq(c.Tables, 0.724) {
					t.Errorf("経路 A の title/tables = %s/%s, want Textract の値そのまま", fmtPtr(c.Title), fmtPtr(c.Tables))
				}
			} else if c.Title != nil || c.Tables != nil || c.Figures != nil || c.Sections != nil {
				t.Errorf("経路 B で根拠の無いフィールドが埋まった: %+v", c)
			}
		})
	}
}

// Crossref が応答しない場合も処理が完了し、続けて失敗したら残りを問い合わせずに unavailable にすること
func TestVerifyCrossrefUnavailable(t *testing.T) {
	fr := &fakeResolver{}
	v := New(fr, WithClock(fixedClock))
	out, report := v.Verify(context.Background(), sampleDocument(domain.RouteBedrock), sampleOriginal())

	for _, c := range report.References {
		if c.Status != StatusUnavailable {
			t.Errorf("references[%d].Status = %s, want unavailable", c.Index, c.Status)
		}
	}
	if fr.calls != maxConsecutiveUnavailable {
		t.Errorf("resolver calls = %d, want %d (続けて失敗したら見切る)", fr.calls, maxConsecutiveUnavailable)
	}
	if !strings.Contains(report.References[3].Detail, "続けて応答しなかった") {
		t.Errorf("references[3].Detail = %q", report.References[3].Detail)
	}
	if report.Fields.References.Crossref != nil {
		t.Errorf("照合できていないのに Crossref のシグナルがある: %s", fmtPtr(report.Fields.References.Crossref))
	}
	// 原本との一致度だけで信頼度が出ること
	if report.Fields.References.Confidence == nil || !reflect.DeepEqual(report.Fields.References.Confidence, report.Fields.References.Original) {
		t.Errorf("references = %+v, want original のみ", report.Fields.References)
	}
	if !hasWarning(out, "unavailable=6") {
		t.Errorf("警告が無い: %q", out.Provenance.Warnings)
	}
}

// 見切りは連続した失敗にだけ効き、間に成功があれば数え直すこと
func TestVerifyUnavailableStreakResets(t *testing.T) {
	fr := &fakeResolver{}
	fr.lookup = func(doi string) (crossref.Work, error) {
		if fr.calls%3 == 0 {
			return crossref.Work{DOI: doi, Title: "Deep learning"}, nil
		}
		return crossref.Work{}, errors.New("timeout")
	}
	refs := make([]domain.Reference, 0, 9)
	for i := range 9 {
		refs = append(refs, domain.Reference{Raw: "Deep learning", DOI: domain.Ptr(fmt.Sprintf("10.1000/%d", i))})
	}
	v := New(fr)
	_, report := v.Verify(context.Background(), domain.Document{References: refs}, sampleOriginal())

	if fr.calls != 9 {
		t.Errorf("resolver calls = %d, want 9 (2 件失敗 → 1 件成功の繰り返しでは見切らない)", fr.calls)
	}
	if report.Crossref.Verified != 3 || report.Crossref.Unavailable != 6 {
		t.Errorf("summary = %+v", report.Crossref)
	}
}

func TestVerifyNilResolver(t *testing.T) {
	v := New(nil, WithClock(fixedClock))
	out, report := v.Verify(context.Background(), sampleDocument(domain.RouteBedrock), sampleOriginal())

	if report.Crossref.Skipped != 6 || report.Fields.References.Crossref != nil {
		t.Errorf("summary = %+v, crossref = %s", report.Crossref, fmtPtr(report.Fields.References.Crossref))
	}
	if !hasWarning(out, "Resolver 未設定") {
		t.Errorf("警告が無い: %q", out.Provenance.Warnings)
	}
}

// 参照文献が上限を超えた分は問い合わせず skipped にし、件数を警告に書くこと
func TestVerifyLookupCap(t *testing.T) {
	fr := &fakeResolver{lookup: func(doi string) (crossref.Work, error) { return crossref.Work{DOI: doi, Title: "x"}, nil }}
	refs := make([]domain.Reference, 0, MaxLookups+2)
	for i := range MaxLookups + 2 {
		refs = append(refs, domain.Reference{Raw: "x", DOI: domain.Ptr(fmt.Sprintf("10.1000/%d", i))})
	}
	v := New(fr)
	out, report := v.Verify(context.Background(), domain.Document{References: refs}, Original{})

	if fr.calls != MaxLookups {
		t.Errorf("resolver calls = %d, want %d", fr.calls, MaxLookups)
	}
	if report.Crossref.Skipped != 2 || report.References[MaxLookups].Status != StatusSkipped || !strings.Contains(report.References[MaxLookups].Detail, "上限") {
		t.Errorf("summary = %+v, last = %+v", report.Crossref, report.References[MaxLookups])
	}
	if !hasWarning(out, fmt.Sprintf("上限 %d 件を超えたため 2 件", MaxLookups)) {
		t.Errorf("警告が無い: %q", out.Provenance.Warnings)
	}
}

// ctx がキャンセルされたら残りの参照文献を unavailable にして戻ること
func TestVerifyContextCanceled(t *testing.T) {
	fr := &fakeResolver{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, report := New(fr).Verify(ctx, sampleDocument(domain.RouteBedrock), sampleOriginal())

	if fr.calls != 0 {
		t.Errorf("resolver calls = %d, want 0", fr.calls)
	}
	if report.Crossref.Unavailable != 6 || !strings.Contains(report.References[0].Detail, "canceled") {
		t.Errorf("summary = %+v, first = %+v", report.Crossref, report.References[0])
	}
}

func TestVerifyDoesNotMutateInput(t *testing.T) {
	in := sampleDocument(domain.RouteTextract)
	before, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	out, _ := New(nil).Verify(context.Background(), in, sampleOriginal())
	*out.Provenance.Confidence.Title = 0
	out.Provenance.Warnings[0] = "changed"

	after, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("入力が変更された\nbefore: %s\n after: %s", before, after)
	}
}

// 該当要素が無いフィールドは 0 ではなく nil のままにし、JSON でもキーが省かれること
func TestVerifyKeepsNilForMissingElements(t *testing.T) {
	doc := domain.Document{
		Metadata:   domain.Metadata{Title: "Sparse Attention Routing for Long-Context Language Models"},
		Sections:   []domain.Section{},
		Figures:    []domain.Figure{},
		Tables:     []domain.Table{},
		References: []domain.Reference{},
		Provenance: domain.Provenance{Route: domain.RouteBedrock},
	}
	out, report := New(nil, WithReviewThreshold(0.5)).Verify(context.Background(), doc, sampleOriginal())

	c := out.Provenance.Confidence
	if !ptrEq(c.Title, 1) || c.Authors != nil || c.Abstract != nil || c.Sections != nil || c.Figures != nil || c.Tables != nil || c.References != nil {
		t.Errorf("confidence = %+v, want title のみ", c)
	}
	b, err := json.Marshal(out.Provenance.Confidence)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"title":1}` {
		t.Errorf("confidence JSON = %s", b)
	}
	if report.NeedsReview {
		t.Errorf("根拠の無いフィールドが nil のままならレビュー要にならないこと: %+v", report)
	}

	// report 側は null として残す (どのフィールドに根拠が無かったかを読めるようにするため)
	b, err = json.Marshal(report.Fields.Figures)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"confidence":null,"items":0}` {
		t.Errorf("figures の report JSON = %s", b)
	}
}

func TestVerifyReviewThreshold(t *testing.T) {
	// ハルシネーションを含まず、sections だけが低くなる文書 (本文は長い値なのでハルシネーションにはならない)
	// 見出し 1.0 と本文 2/6 (原本の語彙で書かれた別の文) の平均 = 0.6667
	doc := domain.Document{
		Metadata: domain.Metadata{Title: "Sparse Attention Routing for Long-Context Language Models"},
		Sections: []domain.Section{{Level: 1, Heading: "1 Introduction", Text: "a sparse routing head selects query pairs"}},
	}

	tests := map[string]struct {
		opts []Option
		want bool
	}{
		"正常系_既定の閾値 0.7 の場合_sections 0.6667 でレビュー要になること": {want: true},
		"正常系_閾値を 0.5 に下げた場合_レビュー不要になること":                {opts: []Option{WithReviewThreshold(0.5)}, want: false},
		"境界値_閾値がちょうど 0.6667 の場合_未満ではないのでレビュー不要になること":    {opts: []Option{WithReviewThreshold(0.6667)}, want: false},
		"境界値_閾値が 0.6668 の場合_未満になりレビュー要になること":            {opts: []Option{WithReviewThreshold(0.6668)}, want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, report := New(nil, tt.opts...).Verify(context.Background(), doc, sampleOriginal())
			if len(report.Hallucinations) != 0 || !ptrEq(report.Fields.Sections.Confidence, 0.6667) || !ptrEq(report.Fields.Title.Confidence, 1) {
				t.Fatalf("テストの前提が崩れている: %+v", report)
			}
			if report.NeedsReview != tt.want {
				t.Errorf("needsReview = %v, want %v (threshold=%v)", report.NeedsReview, tt.want, report.ReviewThreshold)
			}
		})
	}
}

// ハルシネーションが 1 件でもあればフィールドの信頼度によらずレビュー要になること
func TestVerifyHallucinationForcesReview(t *testing.T) {
	doc := sampleDocument(domain.RouteBedrock)
	doc.Metadata.Authors = doc.Metadata.Authors[:2]
	doc.References = nil

	_, report := New(nil, WithReviewThreshold(0)).Verify(context.Background(), doc, sampleOriginal())
	if got := hallucinationPaths(report); !reflect.DeepEqual(got, []string{"figures[1].caption"}) {
		t.Fatalf("hallucinations = %v", got)
	}
	if !report.NeedsReview {
		t.Error("needsReview = false, want true")
	}

	doc.Figures = doc.Figures[:1]
	_, report = New(nil, WithReviewThreshold(0)).Verify(context.Background(), doc, sampleOriginal())
	if len(report.Hallucinations) != 0 || report.NeedsReview {
		t.Errorf("ハルシネーションが無く閾値 0 ならレビュー不要のはず: %+v", report)
	}
}

// report の JSON が lowerCamelCase のキーで comparison.json に書ける形であることを確かめる
func TestReportJSON(t *testing.T) {
	_, report := New(replayResolver(t), WithClock(fixedClock)).Verify(context.Background(), sampleDocument(domain.RouteBedrock), sampleOriginal())
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"jobId"`, `"verifiedAt"`, `"originalChecked"`, `"fields":{"title":{"confidence":1,"original":1,"items":1}`, `"hallucinations":[{"path":"metadata.authors[2].name","value":"John Doe","score":0}`, `"references":[{"index":0,"doi":"10.1038/nature14539","status":"verified","matchedDoi"`, `"crossref":{"verified":2,"mismatch":1,"notFound":2,"unavailable":1,"skipped":0}`, `"needsReview":true,"reviewThreshold":0.7`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON に %s が無い:\n%s", key, b)
		}
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("この文書では null が現れないはず:\n%s", b)
	}
}
