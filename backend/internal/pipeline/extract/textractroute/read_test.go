package textractroute_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/textractroute"
)

// recordedDir は記録済み AWS レスポンスの置き場所 (backend/testdata)
const recordedDir = "../../../../testdata/textract"

// fixtureDir は本パッケージ内で完結する合成フィクスチャの置き場所
const fixtureDir = "testdata"

// analyze は記録またはフィクスチャを Replayer 経由で読み込み、Client のページング処理を通した結果を返す
func analyze(t *testing.T, path string) *textract.AnalysisResult {
	t.Helper()

	rec, err := textract.LoadRecording(path)
	if err != nil {
		t.Fatalf("LoadRecording: %v", err)
	}
	replayer, err := textract.NewReplayer(rec)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	res, err := textract.New(replayer).GetDocumentAnalysis(context.Background(), rec.JobID)
	if err != nil {
		t.Fatalf("GetDocumentAnalysis: %v", err)
	}
	return res
}

func read(t *testing.T, path string) *textractroute.Reading {
	t.Helper()

	r, err := textractroute.Read(analyze(t, path))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return r
}

func texts(elems []textractroute.Element) []string {
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		out = append(out, e.Text)
	}
	return out
}

// 記録済みレスポンスをそのまま解釈できることを確かめる
//
// この記録は手書きの最小サンプルであり二段組も多段ヘッダーも含まないため、そこは testdata/ の合成フィクスチャで検証する
func TestReadRecordedSample(t *testing.T) {
	t.Parallel()

	r := read(t, textract.RecordingPath(recordedDir, "1706.03762"))

	if r.PageCount != 2 {
		t.Errorf("PageCount = %d, want 2 (DocumentMetadata のページ数であること)", r.PageCount)
	}
	if got := texts(r.Elements); !slices.Equal(got, []string{"Attention Is All You Need", ""}) {
		t.Errorf("elements = %q", got)
	}
	if r.Elements[0].Type != types.BlockTypeLayoutTitle {
		t.Errorf("先頭要素の型 = %q, want LAYOUT_TITLE", r.Elements[0].Type)
	}
	if got, want := r.Elements[0].BBox, (domain.BBox{0.19, 0.08, 0.81, 0.12}); !bboxNear(got, want) {
		t.Errorf("BBox = %v, want %v", got, want)
	}
	if got := r.Elements[0].Confidence; got < 0.96 || got > 0.97 {
		t.Errorf("Confidence = %v, want 0.965 前後 (0.0 - 1.0 へ正規化されていること)", got)
	}

	if len(r.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(r.Tables))
	}
	tbl := r.Tables[0]
	if tbl.Data.ID != "table-1" || tbl.Data.Page != 2 {
		t.Errorf("table = %+v", tbl.Data)
	}
	// 記録のセルは WORD を持たないため、行列だけが復元される
	if !slices.EqualFunc(tbl.Data.Header, [][]string{{""}}, slices.Equal) || !slices.EqualFunc(tbl.Data.Rows, [][]string{{""}}, slices.Equal) {
		t.Errorf("header = %v, rows = %v", tbl.Data.Header, tbl.Data.Rows)
	}

	if c := r.Confidence(); c.Title == nil || c.Tables == nil {
		t.Errorf("confidence = %+v, want title と tables が埋まること", c)
	} else if c.Sections != nil || c.Authors != nil {
		t.Errorf("confidence = %+v, want 裏付けの無い項目は nil のままであること", c)
	}
}

// 二段組のページで Textract が返した順を左段から右段へ並べ直せることを確かめる
func TestReadRestoresTwoColumnOrder(t *testing.T) {
	t.Parallel()

	r := read(t, filepath.Join(fixtureDir, "two-column.json"))

	want := []string{
		"Attention Is All You Need",
		"left one",
		"left two",
		"right one",
		"right two",
		"attention weights",
		"Figure 1: The Transformer architecture.",
	}
	if got := texts(r.Elements); !slices.Equal(got, want) {
		t.Fatalf("読み順 = %q, want %q", got, want)
	}

	wantColumn := []int{0, 1, 1, 2, 2, 0, 0}
	for i, e := range r.Elements {
		if e.Column != wantColumn[i] {
			t.Errorf("elements[%d] (%q) の段 = %d, want %d", i, e.Text, e.Column, wantColumn[i])
		}
	}

	if !slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.HasPrefix(w, "page 1: 二段組") }) {
		t.Errorf("warnings = %q, want 並べ替えを記録した警告", r.Warnings)
	}

	if len(r.Figures) != 1 {
		t.Fatalf("figures = %d, want 1", len(r.Figures))
	}
	fig := r.Figures[0].Data
	if fig.ID != "figure-1" || fig.Page != 2 {
		t.Errorf("figure = %+v", fig)
	}
	if fig.Caption != "Figure 1: The Transformer architecture." {
		t.Errorf("caption = %q", fig.Caption)
	}
	if want := (domain.BBox{0.12, 0.15, 0.88, 0.45}); !bboxNear(fig.BBox, want) {
		t.Errorf("bbox = %v, want %v", fig.BBox, want)
	}
}

// 単段のページでは Textract が返した読み順に手を入れないことを確かめる
func TestReadKeepsSingleColumnOrder(t *testing.T) {
	t.Parallel()

	r := read(t, filepath.Join(fixtureDir, "two-column.json"))

	for _, w := range r.Warnings {
		if strings.HasPrefix(w, "page 2:") {
			t.Errorf("warnings に %q が含まれる (単段のページを並べ替えてはならない)", w)
		}
	}
}

func TestDetectColumns(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		boxes []domain.BBox
		want  bool
	}{
		"正常系_中央に余白のある二段組の場合_二段と判定されること": {
			boxes: []domain.BBox{{0.08, 0.3, 0.46, 0.4}, {0.52, 0.3, 0.90, 0.4}},
			want:  true,
		},
		"正常系_全幅の要素しかない場合_二段と判定されないこと": {
			boxes: []domain.BBox{{0.1, 0.1, 0.9, 0.2}, {0.1, 0.3, 0.9, 0.4}},
			want:  false,
		},
		"異常系_余白が中央から外れている場合_二段と判定されないこと": {
			boxes: []domain.BBox{{0.02, 0.3, 0.10, 0.4}, {0.20, 0.3, 0.55, 0.4}},
			want:  false,
		},
		"境界値_余白が下限に満たない場合_二段と判定されないこと": {
			boxes: []domain.BBox{{0.08, 0.3, 0.49, 0.4}, {0.50, 0.3, 0.90, 0.4}},
			want:  false,
		},
		"境界値_矩形が 1 件しかない場合_二段と判定されないこと": {
			boxes: []domain.BBox{{0.08, 0.3, 0.46, 0.4}},
			want:  false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := textractroute.DetectColumns(tt.boxes)
			if got.TwoColumn != tt.want {
				t.Fatalf("TwoColumn = %v, want %v (split = %v)", got.TwoColumn, tt.want, got.Split)
			}
		})
	}
}

func TestColumnsColumn(t *testing.T) {
	t.Parallel()

	cols := textractroute.Columns{Split: 0.49, TwoColumn: true}

	tests := map[string]struct {
		box  domain.BBox
		want int
	}{
		"正常系_境界より左に収まる場合_左段になること":   {box: domain.BBox{0.08, 0.3, 0.46, 0.4}, want: 1},
		"正常系_境界より右に収まる場合_右段になること":   {box: domain.BBox{0.52, 0.3, 0.90, 0.4}, want: 2},
		"境界値_境界を跨ぐ場合_全幅要素として扱われること": {box: domain.BBox{0.10, 0.3, 0.90, 0.4}, want: 0},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := cols.Column(tt.box); got != tt.want {
				t.Fatalf("Column = %d, want %d", got, tt.want)
			}
		})
	}
}

// 多段ヘッダーと結合セルを含む表が行列を保ったまま取り出せることを確かめる
func TestReadRestoresMultiLevelHeader(t *testing.T) {
	t.Parallel()

	r := read(t, filepath.Join(fixtureDir, "tables-only.json"))

	if len(r.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(r.Tables))
	}
	tbl := r.Tables[0].Data

	wantHeader := [][]string{
		{"Model", "BLEU", "BLEU", "Cost", "Cost"},
		{"Model", "EN-DE", "EN-FR", "FLOPs", "Params"},
	}
	if !slices.EqualFunc(tbl.Header, wantHeader, slices.Equal) {
		t.Errorf("header = %v, want %v", tbl.Header, wantHeader)
	}

	wantRows := [][]string{{"Transformer (base)", "27.3", "38.1", "3.3e18", "65M"}}
	if !slices.EqualFunc(tbl.Rows, wantRows, slices.Equal) {
		t.Errorf("rows = %v, want %v", tbl.Rows, wantRows)
	}

	if tbl.Caption != "Table 2: Machine translation quality." {
		t.Errorf("caption = %q", tbl.Caption)
	}
	if tbl.Page != 1 {
		t.Errorf("page = %d, want 1", tbl.Page)
	}

	want := "table-1: ヘッダー 1 段目の列 3 を左隣のセル結合とみなして推定で復元した"
	if !slices.Contains(r.Warnings, want) {
		t.Errorf("warnings = %q, want %q を含むこと", r.Warnings, want)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "空文字で補った") {
			t.Errorf("warnings に %q が含まれる (欠落は推定復元で埋まっているはず)", w)
		}
	}
}

// LAYOUT を含めずに取得した出力でも落ちず、LINE で読み順を代替できることを確かめる
func TestReadFallsBackToLinesWithoutLayout(t *testing.T) {
	t.Parallel()

	r := read(t, filepath.Join(fixtureDir, "tables-only.json"))

	if got := texts(r.Elements); !slices.Equal(got, []string{"Results are summarised below."}) {
		t.Fatalf("elements = %q", got)
	}
	if r.Elements[0].Type != types.BlockTypeLine {
		t.Errorf("型 = %q, want LINE", r.Elements[0].Type)
	}
	if len(r.Figures) != 0 {
		t.Errorf("figures = %d, want 0 (LAYOUT_FIGURE が無いため)", len(r.Figures))
	}
}

// Bedrock へ渡すテキストに読み順の要素と表が漏れなく載ることを確かめる
func TestReadingText(t *testing.T) {
	t.Parallel()

	got := read(t, filepath.Join(fixtureDir, "tables-only.json")).Text()

	for _, want := range []string{
		"## page 1",
		"[LINE] Results are summarised below.",
		"[TABLE table-1]",
		"| Model | BLEU | BLEU | Cost | Cost |",
		"| Model | EN-DE | EN-FR | FLOPs | Params |",
		"| --- | --- | --- | --- | --- |",
		"| Transformer (base) | 27.3 | 38.1 | 3.3e18 | 65M |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Text に %q が含まれない\n---\n%s", want, got)
		}
	}

	layout := read(t, filepath.Join(fixtureDir, "two-column.json")).Text()
	if !strings.Contains(layout, "[TITLE] Attention Is All You Need") {
		t.Errorf("Text に LAYOUT の種別が付いていない\n---\n%s", layout)
	}
	if strings.Index(layout, "left one") > strings.Index(layout, "right one") {
		t.Error("Text が読み順になっていない")
	}
}

func TestReadRejectsEmptyAnalysis(t *testing.T) {
	t.Parallel()

	for name, res := range map[string]*textract.AnalysisResult{
		"異常系_解析結果が nil の場合_エラーになること":    nil,
		"境界値_Block が 1 件も無い場合_エラーになること": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := textractroute.Read(res); !errors.Is(err, textractroute.ErrEmptyAnalysis) {
				t.Fatalf("err = %v, want ErrEmptyAnalysis", err)
			}
		})
	}
}

func bboxNear(got, want domain.BBox) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if d := got[i] - want[i]; d > 1e-6 || d < -1e-6 {
			return false
		}
	}
	return true
}
