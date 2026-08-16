package finalize

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/bedrockparser"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/bedrockroute"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify/crossref"
)

const (
	testBucket = "test-folio-documents"
	testTable  = "test-folio-jobs"
	jobID      = "01JB8X7K2M9QRT4V6WZ3N5PDA0"
	pageCount  = 2

	// crossrefDir は verify の記録済み Crossref 応答 (新しい記録は取りに行かず、参照文献をこの記録に合わせる)
	crossrefDir = "../verify/testdata/crossref"
)

var (
	uploadedAt  = time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	extractedAt = time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC) // extractedAt は経路 A の抽出完了時刻であり、経路 B のページの時刻の起点にもする
	finalizedAt = time.Date(2026, 8, 16, 2, 0, 0, 0, time.UTC)
)

// syntheticLayer は pdftotext の出力を模した合成の原本テキスト (実 PDF 由来ではない)
//
// 両経路のフィクスチャの値がすべて含まれるようにし、揃った文書なら COMPLETED になる基準にする
const syntheticLayer = "Sparse Attention Routing for\nLong-Context Language Models\n\n" +
	"Aiko Tanaka Marcus Feldman\n\n" +
	"Abstract\nLong-context language models suffer from quadratic attention cost. We propose a routing mechanism that selects a sparse subset of key-value pairs per query head.\n\n" +
	"1 Introduction\nTransformer-based language models have scaled context windows from 2K to over 1M tokens.\n\f" +
	"2 Method\nGiven a query vector q and a set of key vectors K, the router computes a relevance score.\n\n" +
	"Figure 1: Overview of the sparse routing architecture.\n\n" +
	"Table 1: Results on long-context retrieval benchmarks.\nModel Speedup\nDense baseline 1.0x\nOurs 6.2x\n\n" +
	"References\n[1] Y. LeCun, Y. Bengio, and G. Hinton. Deep learning. Nature, 521:436-444, 2015.\n" +
	"[2] G. Xiao, Y. Tian, B. Chen, S. Han, and M. Lewis. Efficient streaming language models with attention sinks. In ICLR, 2024.\n"

const (
	title    = "Sparse Attention Routing for Long-Context Language Models"
	abstract = "Long-context language models suffer from quadratic attention cost. We propose a routing mechanism that selects a sparse subset of key-value pairs per query head."
	intro    = "Transformer-based language models have scaled context windows from 2K to over 1M tokens."
	method   = "Given a query vector q and a set of key vectors K, the router computes a relevance score."
	caption  = "Figure 1: Overview of the sparse routing architecture."
	rawLeCun = "Y. LeCun, Y. Bengio, and G. Hinton. Deep learning. Nature, 521:436-444, 2015."
	rawXiao  = "G. Xiao, Y. Tian, B. Chen, S. Han, and M. Lewis. Efficient streaming language models with attention sinks. In ICLR, 2024."
)

// textractDocument は textract-parser が work/ に書く正規化前の結果を模した合成の文書
//
// 題名の改行と表記ゆれは normalize が揃えることを確かめるために残す
// Textract の確信度と時刻・コストは経路 A の Lambda が埋め終えている前提
func textractDocument() domain.Document {
	return domain.Document{
		JobID:         jobID,
		SchemaVersion: domain.SchemaVersion,
		Source:        source(pageCount),
		Metadata: domain.Metadata{
			Title:    "Sparse Attention Routing for\nLong-Context Language Models",
			Authors:  []domain.Author{{Name: "Aiko Tanaka"}, {Name: "Marcus Feldman"}},
			Abstract: abstract,
		},
		Sections: []domain.Section{
			{Level: 1, Heading: "1 Introduction", Text: intro, Pages: []int{1}},
			{Level: 1, Heading: "2 Method", Text: method, Pages: []int{2}},
		},
		Figures: []domain.Figure{{ID: "figure-1", Caption: caption, Page: 2, BBox: domain.BBox{0.1, 0.1, 0.9, 0.4}}},
		Tables: []domain.Table{{
			ID:      "table-1",
			Caption: "Table 1: Results on long-context retrieval benchmarks.",
			Page:    2,
			Header:  [][]string{{"Model", "Speedup"}},
			Rows:    [][]string{{"Dense baseline", "1.0x"}, {"Ours", "6.2x"}},
		}},
		References: []domain.Reference{
			{Raw: rawLeCun, Title: "Deep learning", Year: 2015, DOI: domain.Ptr("10.1038/nature14539")},
			{Raw: rawXiao, Title: "Efficient streaming language models with attention sinks", Year: 2024, DOI: domain.Ptr("https://doi.org/10.48550/ARXIV.2309.17453")},
		},
		Provenance: domain.Provenance{
			Route:       domain.RouteTextract,
			ExtractedAt: extractedAt,
			Confidence:  domain.Confidence{Title: domain.Ptr(0.998), Sections: domain.Ptr(0.943), Figures: domain.Ptr(0.912), Tables: domain.Ptr(0.724)},
			Cost:        domain.Cost{TextractPages: pageCount, TextractFeatures: []string{"LAYOUT", "TABLES"}, BedrockModel: "model-a", BedrockInputTokens: 4000, BedrockOutputTokens: 600},
			DurationMs:  90000,
			Warnings:    []string{"page 2: 二段組と判定し Textract が返した読み順を左段から右段へ並べ直した"},
		},
	}
}

// bedrockPages は bedrock-parser が work/ に書くページ単位の封筒を模した合成のページ結果
//
// 経路 A と同じ内容だが表を持たず (Counts の差分になる)、題名は大文字小文字だけが違う (Diff の Equal が畳むことの確認)
// page 1 は開始 +4s 完了 +10s、page 2 は開始 +7s 完了 +12s → 壁時計時間は 8000ms
func bedrockPages() []bedrockparser.PageOutput {
	return []bedrockparser.PageOutput{
		{JobID: jobID, Page: 1, ExtractedAt: extractedAt.Add(10 * time.Second), DurationMs: 6000, Result: bedrockroute.PageResult{
			Page:     1,
			Title:    "Sparse attention routing for long-context language models",
			Authors:  []domain.Author{{Name: "Aiko Tanaka"}, {Name: "Marcus Feldman"}},
			Abstract: abstract,
			Sections: []bedrockroute.PageSection{{Level: 1, Heading: "1 Introduction", Text: intro}},
			ModelID:  "model-b",
			Usage:    bedrock.Usage{InputTokens: 3000, OutputTokens: 400},
		}},
		{JobID: jobID, Page: 2, ExtractedAt: extractedAt.Add(12 * time.Second), DurationMs: 5000, Result: bedrockroute.PageResult{
			Page:     2,
			Sections: []bedrockroute.PageSection{{Level: 1, Heading: "2 Method", Text: method}},
			Figures:  []bedrockroute.PageFigure{{Label: "Figure 1", Caption: caption}},
			References: []bedrockroute.PageReference{
				{Raw: rawLeCun, Title: "Deep learning", Year: 2015, DOI: "10.1038/nature14539"},
				{Raw: rawXiao, Title: "Efficient streaming language models with attention sinks", Year: 2024, DOI: "10.48550/arXiv.2309.17453"},
			},
			ModelID: "model-b",
			Usage:   bedrock.Usage{InputTokens: 3500, OutputTokens: 500},
		}},
	}
}

func source(pages int) domain.Source {
	return domain.Source{
		Bucket:       testBucket,
		Key:          s3.OriginalPDFKey(jobID),
		Filename:     s3.OriginalPDFKey(jobID),
		SHA256:       jobID,
		Language:     domain.LanguageEnglish,
		PageCount:    pages,
		HasTextLayer: true,
		UploadedAt:   uploadedAt,
	}
}

func input() Input {
	return Input{JobID: jobID, PageCount: pageCount, HasTextLayer: true, Language: domain.LanguageEnglish}
}

// env はフェイクと再生だけで組み立てたハンドラ一式
type env struct {
	handler *Handler
	docs    *spyS3
	jobs    *fakeDynamo
}

// newEnv はハンドラを組み立てる (Crossref は verify の記録を再生し、実時間を待たない)
func newEnv(t *testing.T) *env {
	t.Helper()

	replayer, err := crossref.NewReplayer(crossrefDir)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	resolver := crossref.New(
		crossref.WithTransport(replayer),
		crossref.WithSleeper(func(context.Context, time.Duration) error { return nil }),
	)
	clock := func() time.Time { return finalizedAt }

	e := &env{docs: &spyS3{Fake: s3test.NewFake()}, jobs: newFakeDynamo()}
	e.jobs.seed(jobID, dynamo.StatusProcessing)
	e.handler = New(
		s3.New(e.docs, testBucket),
		dynamo.New(e.jobs, testTable),
		verify.New(resolver, verify.WithClock(clock)),
		WithClock(clock),
	)
	return e
}

func (e *env) seedPDF(t *testing.T) {
	t.Helper()
	e.docs.Seed(testBucket, s3.OriginalPDFKey(jobID), s3test.Object{Body: []byte("%PDF-1.7\n"), LastModified: uploadedAt})
}

func (e *env) seedLayer(t *testing.T) {
	t.Helper()
	e.docs.Seed(testBucket, s3.TextLayerKey(jobID), s3test.Object{Body: []byte(syntheticLayer), ContentType: "text/plain; charset=utf-8"})
}

func (e *env) seedJSON(t *testing.T, key string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	e.docs.Seed(testBucket, key, s3test.Object{Body: b, ContentType: s3.ContentTypeJSON})
}

func (e *env) seedTextract(t *testing.T, doc domain.Document) {
	t.Helper()
	e.seedJSON(t, s3.TextractDocumentKey(jobID), doc)
}

func (e *env) seedBedrock(t *testing.T, pages []bedrockparser.PageOutput) {
	t.Helper()
	for _, p := range pages {
		e.seedJSON(t, s3.BedrockPageResultKey(jobID, p.Page), p)
	}
}

// seedAll は両経路が成功した状態を配置する
func (e *env) seedAll(t *testing.T) {
	t.Helper()
	e.seedPDF(t)
	e.seedLayer(t)
	e.seedTextract(t, textractDocument())
	e.seedBedrock(t, bedrockPages())
}

func (e *env) object(t *testing.T, key string, v any) []byte {
	t.Helper()
	obj, ok := e.docs.Object(testBucket, key)
	if !ok {
		t.Fatalf("%s が保存されていない", key)
	}
	if obj.ContentType != s3.ContentTypeJSON {
		t.Errorf("%s の ContentType = %q, want %q", key, obj.ContentType, s3.ContentTypeJSON)
	}
	if err := json.Unmarshal(obj.Body, v); err != nil {
		t.Fatalf("%s を復元できない: %v", key, err)
	}
	return obj.Body
}

func (e *env) assertAbsent(t *testing.T, key string) {
	t.Helper()
	if _, ok := e.docs.Object(testBucket, key); ok {
		t.Errorf("%s が保存されている", key)
	}
}

func (e *env) comparison(t *testing.T) Comparison {
	t.Helper()
	var cmp Comparison
	e.object(t, s3.ComparisonKey(jobID), &cmp)
	return cmp
}

func (e *env) fetched(key string) bool {
	return slices.Contains(e.docs.gets, key)
}

func ptrEq(p *float64, want float64) bool {
	return p != nil && *p == want
}

func hasPrefixed(warnings []string, prefix, substr string) bool {
	for _, w := range warnings {
		if strings.HasPrefix(w, prefix) && strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func assertSmall(t *testing.T, label string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", label, err)
	}
	// State 間の上限 256KB に対し S3 キーと判定だけの出力は 1KB にも満たない
	if len(b) > 1024 {
		t.Errorf("%s = %d bytes, want 1KB 未満: %s", label, len(b), b)
	}
}

// 両経路が揃った文書を通し、3 つの成果物と DynamoDB の状態が揃うことを確かめる
func TestHandleBothRoutesSucceed(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedAll(t)

	out, err := e.handler.Handle(context.Background(), input())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	want := Output{
		JobID:             jobID,
		Status:            dynamo.StatusCompleted,
		NeedsReview:       false,
		ResultTextractKey: s3.ResultTextractKey(jobID),
		ResultBedrockKey:  s3.ResultBedrockKey(jobID),
		ComparisonKey:     s3.ComparisonKey(jobID),
	}
	if out != want {
		t.Errorf("Handle() = %+v, want %+v", out, want)
	}
	assertSmall(t, "output", out)
	encoded, _ := json.Marshal(out)
	for _, key := range []string{`"jobId"`, `"status":"COMPLETED"`, `"needsReview":false`, `"resultTextractKey"`, `"resultBedrockKey"`, `"comparisonKey"`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("output JSON に %s が無い: %s", key, encoded)
		}
	}

	// 経路 A: 正規化で題名の改行が畳まれ、Textract の確信度と原本の一致度が平均されて信頼度になること
	var a domain.Document
	e.object(t, s3.ResultTextractKey(jobID), &a)
	if a.Provenance.Route != domain.RouteTextract || a.Metadata.Title != title {
		t.Errorf("経路 A = route %q, title %q", a.Provenance.Route, a.Metadata.Title)
	}
	if !ptrEq(a.Provenance.Confidence.Title, 0.999) || !ptrEq(a.Provenance.Confidence.Tables, 0.862) || !ptrEq(a.Provenance.Confidence.References, 1) {
		t.Errorf("経路 A の confidence = %+v, want title (1+0.998)/2, tables (1+0.724)/2, references 1", a.Provenance.Confidence)
	}
	if !a.Provenance.ExtractedAt.Equal(extractedAt) || a.Provenance.DurationMs != 90000 {
		t.Errorf("経路 A の extractedAt/durationMs = %v/%d (textract-parser の値をそのまま残すこと)", a.Provenance.ExtractedAt, a.Provenance.DurationMs)
	}
	if a.Provenance.Warnings[0] != "page 2: 二段組と判定し Textract が返した読み順を左段から右段へ並べ直した" || !hasPrefixed(a.Provenance.Warnings, "verify: ", "notFound=1") {
		t.Errorf("経路 A の warnings = %q (経路の警告を残し verify の警告を足すこと)", a.Provenance.Warnings)
	}
	if got := *a.References[1].DOI; got != "10.48550/arxiv.2309.17453" {
		t.Errorf("経路 A の references[1].doi = %q (normalize が URL 接頭辞を剥がして小文字にすること)", got)
	}

	// 経路 B: Merge が埋めない時刻・所要時間・信頼度を finalizer が埋め、コストはページの合算になること
	var b domain.Document
	e.object(t, s3.ResultBedrockKey(jobID), &b)
	if b.Provenance.Route != domain.RouteBedrock || !reflect.DeepEqual(b.Source, source(pageCount)) {
		t.Errorf("経路 B = route %q, source %+v", b.Provenance.Route, b.Source)
	}
	if !b.Provenance.ExtractedAt.Equal(extractedAt.Add(12 * time.Second)) {
		t.Errorf("経路 B の extractedAt = %v, want 最も遅いページの完了 %v", b.Provenance.ExtractedAt, extractedAt.Add(12*time.Second))
	}
	if b.Provenance.DurationMs != 8000 {
		t.Errorf("経路 B の durationMs = %d, want 8000 (最も早い開始から最も遅い完了まで)", b.Provenance.DurationMs)
	}
	if !ptrEq(b.Provenance.Confidence.Title, 1) || !ptrEq(b.Provenance.Confidence.Authors, 1) || b.Provenance.Confidence.Tables != nil {
		t.Errorf("経路 B の confidence = %+v, want title 1, authors 1, tables nil", b.Provenance.Confidence)
	}
	if c := b.Provenance.Cost; c.BedrockModel != "model-b" || c.BedrockInputTokens != 6500 || c.BedrockOutputTokens != 900 || c.TextractPages != 0 {
		t.Errorf("経路 B の cost = %+v", c)
	}
	if hasPrefixed(b.Provenance.Warnings, "normalize: ", "extractedAt") {
		t.Errorf("経路 B の extractedAt が埋まっていない: %q", b.Provenance.Warnings)
	}

	// comparison.json: 経路ごとの所在と verify の根拠、両経路の値の並び
	cmp := e.comparison(t)
	if cmp.JobID != jobID || !cmp.FinalizedAt.Equal(finalizedAt) || cmp.Status != dynamo.StatusCompleted || cmp.NeedsReview {
		t.Errorf("comparison のヘッダ = %+v", cmp)
	}
	for route, r := range map[string]RouteResult{"textract": cmp.Routes.Textract, "bedrock": cmp.Routes.Bedrock} {
		if r.Status != RouteSucceeded || r.Error != "" || r.NeedsReview || r.Report == nil || r.Cost == nil || r.DurationMs == 0 {
			t.Errorf("routes.%s = %+v", route, r)
		}
	}
	if cmp.Routes.Textract.ResultKey != s3.ResultTextractKey(jobID) || cmp.Routes.Bedrock.ResultKey != s3.ResultBedrockKey(jobID) {
		t.Errorf("routes の resultKey = %q / %q", cmp.Routes.Textract.ResultKey, cmp.Routes.Bedrock.ResultKey)
	}
	if cmp.Routes.Textract.Report.Route != domain.RouteTextract || cmp.Routes.Bedrock.Report.Route != domain.RouteBedrock || !cmp.Routes.Textract.Report.OriginalChecked {
		t.Errorf("report のヘッダ = %+v / %+v", cmp.Routes.Textract.Report, cmp.Routes.Bedrock.Report)
	}
	if cmp.Routes.Textract.Report.Crossref != (verify.CrossrefSummary{Verified: 1, NotFound: 1}) {
		t.Errorf("crossref summary = %+v, want verified 1, notFound 1 (記録の再生)", cmp.Routes.Textract.Report.Crossref)
	}
	if cmp.Diff == nil {
		t.Fatal("両経路が揃っているのに diff が null")
	}
	d := cmp.Diff
	if !d.Title.Equal || d.Title.Textract != title || d.Title.Bedrock != "Sparse attention routing for long-context language models" {
		t.Errorf("diff.title = %+v (大文字小文字の違いは畳んで一致とすること)", d.Title)
	}
	if !d.Authors.Equal || !d.Abstract.Equal || !d.Headings.Equal || !reflect.DeepEqual(d.Headings.Textract, []string{"1 Introduction", "2 Method"}) {
		t.Errorf("diff = authors %+v, abstract equal %v, headings %+v", d.Authors, d.Abstract.Equal, d.Headings)
	}
	if d.Counts.Tables != (CountPair{Textract: 1, Bedrock: 0, Equal: false}) || d.Counts.References != (CountPair{Textract: 2, Bedrock: 2, Equal: true}) {
		t.Errorf("diff.counts = %+v", d.Counts)
	}

	// DynamoDB: COMPLETED に更新され、errorReason は残らない
	if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusCompleted) {
		t.Errorf("DynamoDB の status = %q, want COMPLETED", got)
	}
	if e.jobs.has(jobID, "errorReason") {
		t.Error("成功したのに errorReason が残っている")
	}
}

// 片方の経路の verify がレビュー要なら全体もレビュー要になり、DynamoDB と出力の判定が揃うことを確かめる
func TestHandleReviewPendingWhenRouteNeedsReview(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedPDF(t)
	e.seedLayer(t)
	e.seedTextract(t, textractDocument())
	// 経路 B にだけ原本に無い著者を混ぜる (ハルシネーション → 経路 B の report.needsReview が true)
	pages := bedrockPages()
	pages[0].Result.Authors = append(pages[0].Result.Authors, domain.Author{Name: "John Doe"})
	e.seedBedrock(t, pages)

	out, err := e.handler.Handle(context.Background(), input())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if out.Status != dynamo.StatusReviewPending || !out.NeedsReview {
		t.Errorf("Handle() = %+v, want REVIEW_PENDING / needsReview", out)
	}

	cmp := e.comparison(t)
	if cmp.Routes.Textract.NeedsReview || !cmp.Routes.Bedrock.NeedsReview {
		t.Errorf("routes の needsReview = textract %v, bedrock %v, want false / true", cmp.Routes.Textract.NeedsReview, cmp.Routes.Bedrock.NeedsReview)
	}
	if cmp.Status != dynamo.StatusReviewPending || !cmp.NeedsReview {
		t.Errorf("comparison = status %q, needsReview %v", cmp.Status, cmp.NeedsReview)
	}
	if cmp.Diff == nil || cmp.Diff.Authors.Equal {
		t.Errorf("diff.authors = %+v, want 一致しない", cmp.Diff)
	}
	if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusReviewPending) {
		t.Errorf("DynamoDB の status = %q, want REVIEW_PENDING", got)
	}
}

// 経路 A だけが失敗した場合は経路 B の結果を保持し、失敗の理由を comparison.json に残してレビュー要にすることを確かめる
func TestHandleTextractFailed(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		catch     string
		wantError string
		wantCause string
	}{
		"正常系_Catch の理由がある場合_その Error と Cause が残ること": {
			catch:     `{"Error":"TextractJobFailed","Cause":"{\"jobId\":\"` + jobID + `\",\"message\":\"textract job ended with status FAILED\"}"}`,
			wantError: "TextractJobFailed",
			wantCause: "textract job ended with status FAILED",
		},
		"正常系_Catch の理由が無い場合_結果が見つからない旨が残ること": {
			wantError: ErrorResultNotFound,
			wantCause: s3.TextractDocumentKey(jobID),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedPDF(t)
			e.seedLayer(t)
			e.seedBedrock(t, bedrockPages())
			in := input()
			in.Textract = json.RawMessage(tt.catch)

			out, err := e.handler.Handle(context.Background(), in)
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if out.Status != dynamo.StatusReviewPending || !out.NeedsReview || out.ResultTextractKey != "" || out.ResultBedrockKey != s3.ResultBedrockKey(jobID) {
				t.Errorf("Handle() = %+v", out)
			}

			e.assertAbsent(t, s3.ResultTextractKey(jobID))
			var b domain.Document
			e.object(t, s3.ResultBedrockKey(jobID), &b)

			cmp := e.comparison(t)
			r := cmp.Routes.Textract
			if r.Status != RouteFailed || r.Error != tt.wantError || !strings.Contains(r.Cause, tt.wantCause) || r.ResultKey != "" || r.Report != nil {
				t.Errorf("routes.textract = %+v", r)
			}
			// 経路 B 自体はレビュー不要でも、片方しか揃わなかった事実でレビュー要にする
			if cmp.Routes.Bedrock.Status != RouteSucceeded || cmp.Routes.Bedrock.NeedsReview {
				t.Errorf("routes.bedrock = %+v", cmp.Routes.Bedrock)
			}
			if cmp.Diff != nil {
				t.Errorf("片方だけなのに diff がある: %+v", cmp.Diff)
			}
			if !cmp.NeedsReview || cmp.Status != dynamo.StatusReviewPending {
				t.Errorf("comparison = status %q, needsReview %v", cmp.Status, cmp.NeedsReview)
			}
			if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusReviewPending) {
				t.Errorf("DynamoDB の status = %q, want REVIEW_PENDING", got)
			}
		})
	}
}

// 経路 A を通さない文書 (日本語) は skipped とし、経路 A の S3 を読みに行かないことを確かめる
//
// skipped は設計どおり通していないだけであり、失敗と違ってそれだけではレビューに倒さない (経路 B の判定に従い COMPLETED になる)
func TestHandleSkipTextract(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedAll(t)
	in := input()
	in.SkipTextract = true
	in.Language = domain.LanguageJapanese

	out, err := e.handler.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if out.ResultTextractKey != "" || out.ResultBedrockKey != s3.ResultBedrockKey(jobID) || out.Status != dynamo.StatusCompleted || out.NeedsReview {
		t.Errorf("Handle() = %+v", out)
	}
	if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusCompleted) {
		t.Errorf("DynamoDB の status = %q, want COMPLETED", got)
	}
	if e.fetched(s3.TextractDocumentKey(jobID)) {
		t.Error("skipTextract なのに経路 A の結果を読みに行っている")
	}
	e.assertAbsent(t, s3.ResultTextractKey(jobID))

	cmp := e.comparison(t)
	if r := cmp.Routes.Textract; r.Status != RouteSkipped || r.Error != "" || r.Report != nil {
		t.Errorf("routes.textract = %+v, want skipped", r)
	}
	if cmp.Routes.Bedrock.Status != RouteSucceeded || cmp.Diff != nil {
		t.Errorf("routes.bedrock = %+v, diff = %+v", cmp.Routes.Bedrock, cmp.Diff)
	}
	var b domain.Document
	e.object(t, s3.ResultBedrockKey(jobID), &b)
	if b.Source.Language != domain.LanguageJapanese {
		t.Errorf("経路 B の source.language = %q, want ja (入力をそのまま載せること)", b.Source.Language)
	}
}

// 経路 B の一部のページが無くても続行し、欠落を警告に残すことを確かめる
func TestHandleBedrockPartialPages(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedAll(t)
	// 3 ページの文書として扱い、3 ページ目だけが無い状態にする
	in := input()
	in.PageCount = 3

	out, err := e.handler.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if out.ResultBedrockKey != s3.ResultBedrockKey(jobID) {
		t.Errorf("Handle() = %+v", out)
	}
	if !e.fetched(s3.BedrockPageResultKey(jobID, 3)) {
		t.Error("3 ページ目を読みに行っていない")
	}

	var b domain.Document
	e.object(t, s3.ResultBedrockKey(jobID), &b)
	if !slices.Contains(b.Provenance.Warnings, "ページの抽出結果が欠落したまま結合した: page=3") {
		t.Errorf("経路 B の warnings = %q, want 欠落の警告", b.Provenance.Warnings)
	}
	if b.Source.PageCount != 3 || len(b.Sections) != 2 {
		t.Errorf("経路 B = pageCount %d, sections %d (揃ったページだけで結合すること)", b.Source.PageCount, len(b.Sections))
	}
	cmp := e.comparison(t)
	if cmp.Routes.Bedrock.Status != RouteSucceeded || !slices.Contains(cmp.Routes.Bedrock.Warnings, "ページの抽出結果が欠落したまま結合した: page=3") {
		t.Errorf("routes.bedrock = %+v", cmp.Routes.Bedrock)
	}
}

// 両経路とも失敗した場合は理由を comparison.json と DynamoDB に残し、再試行しても解消しないことを型で示すエラーを返すことを確かめる
func TestHandleBothRoutesFailed(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedPDF(t)
	e.seedLayer(t)
	in := input()
	in.Textract = json.RawMessage(`{"Error":"TextractJobFailed","Cause":"textract job ended with status FAILED"}`)
	in.Bedrock = json.RawMessage(`{"Error":"States.TaskFailed","Cause":"every page failed"}`)

	_, err := e.handler.Handle(context.Background(), in)
	noResult, ok := errors.AsType[*NoResultError](err)
	if !ok {
		t.Fatalf("Handle() error = %T (%v), want *NoResultError", err, err)
	}
	// aws-lambda-go は errorType にポインタの Elem().Name() を用いる (Retry の ErrorEquals はこの名前で照合する)
	if got := reflect.TypeOf(err).Elem().Name(); got != "NoResultError" {
		t.Errorf("errorType = %q, want NoResultError", got)
	}
	if noResult.JobID != jobID {
		t.Errorf("NoResultError.JobID = %q", noResult.JobID)
	}
	for _, want := range []string{"textract: TextractJobFailed: textract job ended with status FAILED", "bedrock: States.TaskFailed: every page failed"} {
		if !strings.Contains(noResult.Reason, want) {
			t.Errorf("NoResultError.Reason = %q, want %q を含む", noResult.Reason, want)
		}
	}

	e.assertAbsent(t, s3.ResultTextractKey(jobID))
	e.assertAbsent(t, s3.ResultBedrockKey(jobID))
	cmp := e.comparison(t)
	if cmp.Status != dynamo.StatusFailed || cmp.NeedsReview || cmp.Diff != nil {
		t.Errorf("comparison = status %q, needsReview %v, diff %+v", cmp.Status, cmp.NeedsReview, cmp.Diff)
	}
	if cmp.Routes.Textract.Status != RouteFailed || cmp.Routes.Textract.Error != "TextractJobFailed" || cmp.Routes.Bedrock.Status != RouteFailed || cmp.Routes.Bedrock.Cause != "every page failed" {
		t.Errorf("routes = %+v", cmp.Routes)
	}

	if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusFailed) {
		t.Errorf("DynamoDB の status = %q, want FAILED", got)
	}
	if got := e.jobs.attr(jobID, "errorReason"); got != noResult.Reason {
		t.Errorf("DynamoDB の errorReason = %q, want %q", got, noResult.Reason)
	}
	if e.jobs.updates != 1 {
		t.Errorf("DynamoDB の更新回数 = %d, want 1 (MarkFailed のみ)", e.jobs.updates)
	}
}

// 経路 A が skipped で経路 B が失敗した場合も両経路が揃わないため NoResultError になることを確かめる
func TestHandleSkippedAndFailed(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedPDF(t)
	in := input()
	in.SkipTextract = true
	in.HasTextLayer = false

	_, err := e.handler.Handle(context.Background(), in)
	noResult, ok := errors.AsType[*NoResultError](err)
	if !ok {
		t.Fatalf("Handle() error = %T (%v), want *NoResultError", err, err)
	}
	if !strings.HasPrefix(noResult.Reason, "textract: skipped; bedrock: "+ErrorResultNotFound) {
		t.Errorf("Reason = %q", noResult.Reason)
	}
	if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusFailed) {
		t.Errorf("DynamoDB の status = %q, want FAILED", got)
	}
}

// テキストレイヤーが無い文書は layer.txt を読まず、verify が突合の省略を警告してレビュー要に倒すことを確かめる
func TestHandleWithoutTextLayer(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedAll(t)
	in := input()
	in.HasTextLayer = false
	// 経路 A の source も前処理の判定に揃える (verify は文書側の hasTextLayer を見る)
	doc := textractDocument()
	doc.Source.HasTextLayer = false
	e.seedTextract(t, doc)

	out, err := e.handler.Handle(context.Background(), in)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if out.Status != dynamo.StatusReviewPending || !out.NeedsReview {
		t.Errorf("Handle() = %+v, want REVIEW_PENDING", out)
	}
	if e.fetched(s3.TextLayerKey(jobID)) {
		t.Error("hasTextLayer=false なのに layer.txt を読みに行っている")
	}

	cmp := e.comparison(t)
	for route, r := range map[string]RouteResult{"textract": cmp.Routes.Textract, "bedrock": cmp.Routes.Bedrock} {
		if r.Status != RouteSucceeded || !r.NeedsReview || r.Report == nil || r.Report.OriginalChecked {
			t.Errorf("routes.%s = %+v, want succeeded かつレビュー要かつ突合なし", route, r)
		}
		if !hasPrefixed(r.Warnings, "verify: ", "テキストレイヤーが無いため") {
			t.Errorf("routes.%s.warnings = %q", route, r.Warnings)
		}
	}
	// 経路 A は Textract の確信度がそのまま信頼度になり、経路 B は参照文献 (Crossref) 以外の根拠を持たない
	var a, b domain.Document
	e.object(t, s3.ResultTextractKey(jobID), &a)
	e.object(t, s3.ResultBedrockKey(jobID), &b)
	if !ptrEq(a.Provenance.Confidence.Title, 0.998) || b.Provenance.Confidence.Title != nil || !ptrEq(b.Provenance.Confidence.References, 1) {
		t.Errorf("confidence = a.title %v, b.title %v, b.references %v", a.Provenance.Confidence.Title, b.Provenance.Confidence.Title, b.Provenance.Confidence.References)
	}
	if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusReviewPending) {
		t.Errorf("DynamoDB の status = %q, want REVIEW_PENDING", got)
	}
}

// あるはずのテキストレイヤーが S3 に無くても止まらず、突合を省略して続行することを確かめる
func TestHandleTextLayerMissing(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedPDF(t)
	e.seedTextract(t, textractDocument())
	e.seedBedrock(t, bedrockPages())

	out, err := e.handler.Handle(context.Background(), input())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if out.Status != dynamo.StatusReviewPending {
		t.Errorf("Handle() = %+v, want REVIEW_PENDING (突合を省略したため)", out)
	}
	if !e.fetched(s3.TextLayerKey(jobID)) {
		t.Error("layer.txt を読みに行っていない")
	}
	cmp := e.comparison(t)
	if cmp.Routes.Textract.Report.OriginalChecked || cmp.Routes.Bedrock.Report.OriginalChecked {
		t.Error("layer.txt が無いのに突合している")
	}
}

// 同じ入力で 2 回実行しても成果物と状態が変わらないことを確かめる
//
// 経路 A の入力を outputs/ ではなく work/ の正規化前の結果にしているのはこのためであり、自分の出力を読み戻すと Textract の確信度が合成済みの値に置き換わり、verify の警告も重複する
func TestHandleIsIdempotent(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedAll(t)

	run := func() (Output, []byte, []byte, []byte) {
		t.Helper()
		out, err := e.handler.Handle(context.Background(), input())
		if err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		var a, b domain.Document
		var cmp Comparison
		return out, e.object(t, s3.ResultTextractKey(jobID), &a), e.object(t, s3.ResultBedrockKey(jobID), &b), e.object(t, s3.ComparisonKey(jobID), &cmp)
	}

	out1, a1, b1, cmp1 := run()
	status1 := e.jobs.attr(jobID, "status")
	out2, a2, b2, cmp2 := run()

	if out1 != out2 {
		t.Errorf("出力が変わった:\n1: %+v\n2: %+v", out1, out2)
	}
	if string(a1) != string(a2) {
		t.Errorf("result-textract.json が変わった:\n1: %s\n2: %s", a1, a2)
	}
	if string(b1) != string(b2) {
		t.Errorf("result-bedrock.json が変わった:\n1: %s\n2: %s", b1, b2)
	}
	if string(cmp1) != string(cmp2) {
		t.Errorf("comparison.json が変わった:\n1: %s\n2: %s", cmp1, cmp2)
	}
	if got := e.jobs.attr(jobID, "status"); got != status1 {
		t.Errorf("DynamoDB の status が変わった: %q → %q", status1, got)
	}

	// 経路 A の信頼度は 2 回目も Textract の確信度と原本の一致度の平均のままであること
	var a domain.Document
	if err := json.Unmarshal(a2, &a); err != nil {
		t.Fatal(err)
	}
	if !ptrEq(a.Provenance.Confidence.Title, 0.999) {
		t.Errorf("2 回目の経路 A の confidence.title = %v, want 0.999", a.Provenance.Confidence.Title)
	}
	if n := strings.Count(strings.Join(a.Provenance.Warnings, "\n"), "verify: "); n != 1 {
		t.Errorf("verify の警告が %d 件ある (重複して追記されている): %q", n, a.Provenance.Warnings)
	}
}

// 入力不正は再試行しても解消しないことを型で示し、DynamoDB と S3 を触らないことを確かめる
func TestHandleRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   Input
		want error
	}{
		"異常系_jobId が空の場合_ErrEmptyJobID を InvalidInputError で返すこと":             {in: Input{PageCount: 1}, want: ErrEmptyJobID},
		"異常系_pageCount が 0 の場合_ErrInvalidPageCount を InvalidInputError で返すこと": {in: Input{JobID: jobID}, want: ErrInvalidPageCount},
		"異常系_pageCount が負の場合_ErrInvalidPageCount を InvalidInputError で返すこと":   {in: Input{JobID: jobID, PageCount: -1}, want: ErrInvalidPageCount},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedAll(t)

			_, err := e.handler.Handle(context.Background(), tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Handle() error = %v, want %v", err, tt.want)
			}
			if _, ok := errors.AsType[*InvalidInputError](err); !ok {
				t.Errorf("Handle() error = %T, want *InvalidInputError", err)
			}
			if e.jobs.updates != 0 {
				t.Errorf("入力不正なのに DynamoDB を %d 回更新している", e.jobs.updates)
			}
			if len(e.docs.gets) != 0 {
				t.Errorf("入力不正なのに S3 を読んでいる: %q", e.docs.gets)
			}
			e.assertAbsent(t, s3.ComparisonKey(jobID))
		})
	}
}

// 途中の予期しない失敗は MarkFailed を試みてから元のエラーを返し、MarkFailed 自体の失敗は元のエラーを覆い隠さないことを確かめる
func TestHandleMarksFailedOnUnexpectedError(t *testing.T) {
	t.Parallel()

	s3Err := errors.New("s3 unavailable")
	dynamoErr := errors.New("dynamo unavailable")

	tests := map[string]struct {
		setup      func(e *env)
		want       error
		wantMarked bool
	}{
		"正常系_S3 の読み取りが失敗した場合_FAILED と理由を記録して元のエラーを返すこと": {
			setup:      func(e *env) { e.docs.GetErr = s3Err },
			want:       s3Err,
			wantMarked: true,
		},
		"正常系_原本の Head が失敗した場合_FAILED と理由を記録して元のエラーを返すこと": {
			setup:      func(e *env) { e.docs.HeadErr = s3Err },
			want:       s3Err,
			wantMarked: true,
		},
		"正常系_成果物の書き込みが失敗した場合_FAILED と理由を記録して元のエラーを返すこと": {
			setup:      func(e *env) { e.docs.PutErr = s3Err },
			want:       s3Err,
			wantMarked: true,
		},
		"正常系_DynamoDB の更新が失敗した場合_MarkFailed も失敗するが元のエラーを返すこと": {
			setup: func(e *env) { e.jobs.err = dynamoErr },
			want:  dynamoErr,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedAll(t)
			tt.setup(e)

			_, err := e.handler.Handle(context.Background(), input())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Handle() error = %v, want %v", err, tt.want)
			}
			if _, ok := errors.AsType[*NoResultError](err); ok {
				t.Errorf("予期しない失敗が NoResultError になっている: %v", err)
			}
			if !tt.wantMarked {
				return
			}
			if got := e.jobs.attr(jobID, "status"); got != string(dynamo.StatusFailed) {
				t.Errorf("DynamoDB の status = %q, want FAILED", got)
			}
			if got := e.jobs.attr(jobID, "errorReason"); !strings.Contains(got, tt.want.Error()) {
				t.Errorf("DynamoDB の errorReason = %q, want %q を含む", got, tt.want.Error())
			}
		})
	}
}
