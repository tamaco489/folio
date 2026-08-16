package finalize

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/bedrockparser"
)

// Catch の結果だけを失敗の理由とみなし、成功時の出力や欠落は理由なしとして無視することを確かめる
func TestParseCatch(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw    string
		want   catch
		wantOK bool
	}{
		"正常系_Catch の Error と Cause がある場合_理由として取り出せること":       {raw: `{"Error":"TextractJobFailed","Cause":"{\"jobId\":\"j\",\"message\":\"textract job ended with status FAILED\"}"}`, want: catch{Error: "TextractJobFailed", Cause: `{"jobId":"j","message":"textract job ended with status FAILED"}`}, wantOK: true},
		"正常系_キーが小文字の場合_大文字小文字を問わず取り出せること":                    {raw: `{"error":"States.TaskFailed","cause":"map failed"}`, want: catch{Error: "States.TaskFailed", Cause: "map failed"}, wantOK: true},
		"正常系_Cause が無い場合_Error だけで理由になること":                   {raw: `{"Error":"States.Timeout"}`, want: catch{Error: "States.Timeout"}, wantOK: true},
		"正常系_経路 A の成功出力 (resultKey を持つオブジェクト) の場合_理由なしになること": {raw: `{"jobId":"j","resultKey":"work/j/textract/document.json","rawKey":"work/j/textract/raw.json"}`},
		"正常系_Map の成功出力 (配列) の場合_理由なしになること":                   {raw: `[{"jobId":"j","page":1,"resultKey":"work/j/bedrock/page-0001.json"}]`},
		"正常系_null の場合_理由なしになること":                             {raw: `null`},
		"正常系_Error が空文字の場合_理由なしになること":                        {raw: `{"Error":"","Cause":"x"}`},
		"正常系_Error が文字列でない場合_理由なしになること":                      {raw: `{"Error":{"code":1},"Cause":"x"}`},
		"境界値_入力が無い場合_理由なしになること":                              {raw: ``},
		"異常系_JSON として読めない場合_理由なしになること":                       {raw: `not json`},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCatch(json.RawMessage(tt.raw))
			if ok != tt.wantOK {
				t.Fatalf("parseCatch(%s) ok = %v, want %v (got %+v)", tt.raw, ok, tt.wantOK, got)
			}
			if got != tt.want {
				t.Errorf("parseCatch(%s) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

// Diff の Equal は空白を畳んで大文字小文字を無視するが、それ以外の差は残すことを確かめる
func TestEqualText(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b string
		want bool
	}{
		"正常系_同じ文字列の場合_一致すること":         {a: "Sparse Attention Routing", b: "Sparse Attention Routing", want: true},
		"正常系_大文字小文字だけが違う場合_一致すること":    {a: "Sparse Attention Routing", b: "sparse attention ROUTING", want: true},
		"正常系_空白の数と改行だけが違う場合_一致すること":   {a: "Sparse  Attention\nRouting", b: " Sparse Attention Routing ", want: true},
		"正常系_全角スペースで区切られている場合_一致すること": {a: "疎な　注意", b: "疎な 注意", want: true},
		"正常系_語が違う場合_一致しないこと":          {a: "Sparse Attention Routing", b: "Dense Attention Routing", want: false},
		"正常系_ハイフンの有無が違う場合_一致しないこと":    {a: "Long-Context", b: "Long Context", want: false},
		"境界値_両方が空の場合_一致すること":          {a: "", b: "", want: true},
		"境界値_片方だけが空の場合_一致しないこと":       {a: "x", b: "", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := equalText(tt.a, tt.b); got != tt.want {
				t.Errorf("equalText(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// 一覧の Equal は要素数と順序を含めて比べ、要素ごとの比較は equalText の規則に従うことを確かめる
func TestEqualList(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b []string
		want bool
	}{
		"正常系_同じ要素が同じ順に並ぶ場合_一致すること":       {a: []string{"1 Introduction", "2 Method"}, b: []string{"1 Introduction", "2 Method"}, want: true},
		"正常系_要素の大文字小文字と空白だけが違う場合_一致すること": {a: []string{"Aiko Tanaka", "Marcus  Feldman"}, b: []string{"aiko tanaka", "Marcus Feldman"}, want: true},
		"正常系_順序が違う場合_一致しないこと":            {a: []string{"a", "b"}, b: []string{"b", "a"}, want: false},
		"正常系_要素数が違う場合_一致しないこと":           {a: []string{"a", "b"}, b: []string{"a"}, want: false},
		"境界値_両方が空の場合_一致すること":             {a: []string{}, b: nil, want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := equalList(tt.a, tt.b); got != tt.want {
				t.Errorf("equalList(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// 経路 B の所要時間は Map 全体の壁時計時間 (最も早い開始から最も遅い完了まで) であり、ページごとの合計や最大ではないことを確かめる
func TestTiming(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	page := func(end time.Duration, durationMs int64) bedrockparser.PageOutput {
		return bedrockparser.PageOutput{ExtractedAt: base.Add(end), DurationMs: durationMs}
	}

	tests := map[string]struct {
		pages      []bedrockparser.PageOutput
		wantAt     time.Time
		wantDurMs  int64
		wantZeroAt bool
	}{
		// page 1: 開始 +4s 完了 +10s、page 2: 開始 +7s 完了 +12s → +4s から +12s の 8000ms (合計 11000、最大 6000、完了の差 2000 のいずれとも異なる)
		"正常系_ページが重なって走る場合_最も早い開始から最も遅い完了までになること": {
			pages:     []bedrockparser.PageOutput{page(10*time.Second, 6000), page(12*time.Second, 5000)},
			wantAt:    base.Add(12 * time.Second),
			wantDurMs: 8000,
		},
		// 最後に完了したページより早く開始したページが先に来ても、開始の最小と完了の最大を別々に取ること
		"正常系_最も早く開始したページが最も遅く完了する場合_その 1 ページの所要時間になること": {
			pages:     []bedrockparser.PageOutput{page(5*time.Second, 1000), page(20*time.Second, 19000)},
			wantAt:    base.Add(20 * time.Second),
			wantDurMs: 19000,
		},
		"正常系_1 ページだけの場合_そのページの所要時間になること": {
			pages:     []bedrockparser.PageOutput{page(3*time.Second, 2500)},
			wantAt:    base.Add(3 * time.Second),
			wantDurMs: 2500,
		},
		"正常系_完了時刻の無いページが混ざる場合_計算から外されること": {
			pages:     []bedrockparser.PageOutput{{DurationMs: 999999}, page(10*time.Second, 6000)},
			wantAt:    base.Add(10 * time.Second),
			wantDurMs: 6000,
		},
		"境界値_完了時刻のあるページが無い場合_未設定のままになること": {
			pages:      []bedrockparser.PageOutput{{DurationMs: 100}},
			wantZeroAt: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			at, durMs := timing(tt.pages)
			if tt.wantZeroAt {
				if !at.IsZero() || durMs != 0 {
					t.Fatalf("timing() = %v, %d, want zero", at, durMs)
				}
				return
			}
			if !at.Equal(tt.wantAt) || at.Location() != time.UTC {
				t.Errorf("extractedAt = %v, want %v (UTC)", at, tt.wantAt)
			}
			if durMs != tt.wantDurMs {
				t.Errorf("durationMs = %d, want %d", durMs, tt.wantDurMs)
			}
		})
	}
}

// Diff は両経路の値を並べるだけで、どちらが正しいかは判定しないことを確かめる
func TestDiff(t *testing.T) {
	t.Parallel()

	a := domain.Document{
		Metadata: domain.Metadata{
			Title:    "Sparse Attention Routing",
			Authors:  []domain.Author{{Name: "Aiko Tanaka", Affiliation: "Tohoku University"}, {Name: "Marcus Feldman"}},
			Abstract: "We propose a routing mechanism.",
		},
		Sections:   []domain.Section{{Heading: "1 Introduction"}, {Heading: "2 Method"}},
		Figures:    []domain.Figure{{ID: "figure-1"}},
		Tables:     []domain.Table{{ID: "table-1"}},
		References: []domain.Reference{{Raw: "r1"}, {Raw: "r2"}},
	}
	b := domain.Document{
		Metadata: domain.Metadata{
			Title:    "sparse attention routing",
			Authors:  []domain.Author{{Name: "Aiko Tanaka"}},
			Abstract: "We propose a routing mechanism.",
		},
		Sections:   []domain.Section{{Heading: "1 Introduction"}, {Heading: ""}, {Heading: "2 Method"}},
		Figures:    []domain.Figure{{ID: "図 1"}},
		References: []domain.Reference{{Raw: "r1"}, {Raw: "r2"}},
	}

	got := diff(a, b)
	want := Diff{
		Title:    ValuePair{Textract: "Sparse Attention Routing", Bedrock: "sparse attention routing", Equal: true},
		Authors:  ListPair{Textract: []string{"Aiko Tanaka", "Marcus Feldman"}, Bedrock: []string{"Aiko Tanaka"}, Equal: false},
		Abstract: ValuePair{Textract: "We propose a routing mechanism.", Bedrock: "We propose a routing mechanism.", Equal: true},
		Counts: Counts{
			Sections:   CountPair{Textract: 2, Bedrock: 3, Equal: false},
			Figures:    CountPair{Textract: 1, Bedrock: 1, Equal: true},
			Tables:     CountPair{Textract: 1, Bedrock: 0, Equal: false},
			References: CountPair{Textract: 2, Bedrock: 2, Equal: true},
		},
		Headings: ListPair{Textract: []string{"1 Introduction", "2 Method"}, Bedrock: []string{"1 Introduction", "", "2 Method"}, Equal: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff() = %+v,\nwant %+v", got, want)
	}

	// 一覧は要素が無くても null ではなく空配列で出力する (経路間で同じ形の差分を取るため)
	encoded, err := json.Marshal(diff(domain.Document{}, domain.Document{}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `"authors":{"textract":[],"bedrock":[],"equal":true}`; !strings.Contains(string(encoded), want) {
		t.Errorf("空の文書の diff = %s, want %s を含む", encoded, want)
	}
}
