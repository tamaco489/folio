package verify

import (
	"reflect"
	"testing"
)

func TestFold(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"正常系_大文字と空白を含む場合_小文字化して空白が除かれること":     {in: "Sparse Attention Routing", want: "sparseattentionrouting"},
		"正常系_行末ハイフネーションの場合_ハイフンなしの語と同じになること":  {in: "long-\ncontext", want: "longcontext"},
		"正常系_ハイフンで繋いだ語の場合_ハイフネーションと同じになること":   {in: "long-context", want: "longcontext"},
		"正常系_合字を含む場合_構成文字に展開されること":            {in: "eﬃcient ﬁne-tuning ﬂow ﬀ ﬄ", want: "efficientfinetuningflowffffl"},
		"正常系_引用符とダッシュの種類が違う場合_どちらも消えて同じになること": {in: "“Attention” — is ‘all’", want: "attentionisall"},
		"正常系_句読点を含む場合_除かれること":                 {in: "Attention Is All You Need.", want: "attentionisallyouneed"},
		"正常系_日本語の場合_文字がそのまま残ること":              {in: "疎な注意経路選択による\n長文脈言語モデル", want: "疎な注意経路選択による長文脈言語モデル"},
		"境界値_空文字の場合_空文字のままであること":              {in: "", want: ""},
		"境界値_記号だけの場合_空文字になること":                {in: " —– ...", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := fold(tt.in); got != tt.want {
				t.Errorf("fold(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := map[string]struct {
		in   string
		want []string
	}{
		"正常系_英語の場合_小文字の単語に分かれること":                    {in: "Sparse Attention-Routing 2K", want: []string{"sparse", "attention", "routing", "2k"}},
		"正常系_合字を含む場合_展開した語になること":                     {in: "eﬃcient", want: []string{"efficient"}},
		"正常系_日本語の場合_文字バイグラムになること":                    {in: "注意機構", want: []string{"注意", "意機", "機構"}},
		"正常系_日本語と英数字が混じる場合_文字種の境界で分かれること":            {in: "文脈長を2Kトークン", want: []string{"文脈", "脈長", "長を", "2k", "トー", "ーク", "クン"}},
		"正常系_分解済みのアクセント付き文字の場合_結合文字を読み飛ばして 1 語になること": {in: "Schrödinger", want: []string{"schrodinger"}},
		"境界値_日本語 1 文字の場合_その文字がトークンになること":             {in: "表", want: []string{"表"}},
		"境界値_空文字の場合_トークンが無いこと":                       {in: "", want: nil},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tokenize(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// pdftotext の出力を模した原本 (行の折り返し、行末ハイフネーション、合字、改ページを含む)
const sampleLayer = "Sparse Attention Routing for\nLong-Context Language Models\n\nAiko Tanaka1 Marcus Feldman2\n\nAbstract\nLong-context language models suﬀer from quadratic atten-\ntion cost. We propose a routing mechanism that selects a\nsparse subset of key-value pairs per query head.\n\f1 Introduction\nThe trans-\nformer remains quadratic in sequence length.\n"

func TestCorpusContains(t *testing.T) {
	c := newCorpus(sampleLayer)
	tests := map[string]struct {
		in   string
		want bool
	}{
		"正常系_行の折り返しを跨ぐ題名の場合_一致すること":               {in: "Sparse Attention Routing for Long-Context Language Models", want: true},
		"正常系_上付き文字が混じった著者名の場合_一致すること":             {in: "Aiko Tanaka", want: true},
		"正常系_ハイフネーションを跨ぐ語の場合_一致すること":              {in: "attention cost", want: true},
		"正常系_合字が展開された語の場合_一致すること":                 {in: "suffer", want: true},
		"正常系_ハイフネーションで割れた 1 語の場合_一致すること":          {in: "transformer", want: true},
		"異常系_原本に無い値の場合_一致しないこと":                   {in: "Dense Retrieval Baselines", want: false},
		"境界値_空文字の場合_一致しないこと (何にでも含まれる空文字を一致にしない)": {in: "", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := c.contains(tt.in); got != tt.want {
				t.Errorf("contains(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCorpusCoverage(t *testing.T) {
	c := newCorpus(sampleLayer)
	tests := map[string]struct {
		in     string
		want   float64
		wantOK bool
	}{
		"正常系_原本と同じ文の場合_1 になること":                  {in: "We propose a routing mechanism that selects a sparse subset of key-value pairs per query head.", want: 1, wantOK: true},
		"正常系_行の折り返しとハイフネーションを跨ぐ場合_1 になること":       {in: "quadratic attention cost", want: 1, wantOK: true},
		"正常系_ハイフンで繋いだ語がハイフネーションで割れている場合_1 になること": {in: "long-context language", want: 1, wantOK: true},
		"正常系_語順が入れ替わっている場合_隣接する対が保たれれば 1 になること":  {in: "mechanism routing", want: 1, wantOK: true},
		"正常系_原本の語彙で書かれた別の文の場合_対が崩れて低くなること":       {in: "a sparse routing head selects query pairs", want: 1.0 / 6, wantOK: true},
		"正常系_一部の語が違う場合_崩れた対の分だけ下がること":            {in: "sparse subset of key-value tuples per query head", want: 6.0 / 8, wantOK: true},
		"正常系_1 語で原本にある場合_1 になること":                {in: "quadratic", want: 1, wantOK: true},
		"正常系_1 語で原本に無い場合_0 になること":                {in: "retrieval", want: 0, wantOK: true},
		"境界値_同じ対が繰り返される場合_1 つに数えること":             {in: "query head query head", want: 1, wantOK: true},
		"境界値_トークンが無い場合_根拠なしになること":                {in: " — ", want: 0, wantOK: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := c.coverage(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("coverage(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("coverage(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// 部分文字列一致が壊れる場合にトークン対の被覆率へ落ちることを確かめる
func TestCorpusShortScore(t *testing.T) {
	c := newCorpus(sampleLayer)

	if got, _ := c.shortScore("Long-Context Language Models"); got != 1 {
		t.Errorf("連続して現れる値の shortScore = %v, want 1", got)
	}
	// 4 語 (3 対) のうち末尾の 1 語が違う: 部分文字列一致は失敗し、対 2/3 が残る
	if got, _ := c.shortScore("Long-Context Language Systems"); got != 2.0/3 {
		t.Errorf("末尾の 1 語が違う値の shortScore = %v, want 2/3", got)
	}
	// 原本側で上付き文字が語に付いた著者名 ("Aiko Tanaka1") はトークンが崩れるが、部分文字列一致で救う
	if got, _ := c.coverage("Aiko Tanaka"); got != 0 {
		t.Fatalf("coverage(Aiko Tanaka) = %v, want 0 (テストの前提が崩れている)", got)
	}
	if got, _ := c.shortScore("Aiko Tanaka"); got != 1 {
		t.Errorf("上付き文字が混じった著者名の shortScore = %v, want 1", got)
	}
	if _, ok := c.shortScore(""); ok {
		t.Error("空文字が根拠ありと判定された")
	}
}
