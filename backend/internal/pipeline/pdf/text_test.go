package pdf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountText(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		text      string
		wantChars int
		wantPages int
	}{
		"境界値_空文字の場合_0 文字 1 ページになること":      {text: "", wantChars: 0, wantPages: 1},
		"正常系_空白を含む場合_空白を除いた文字数になること":      {text: "a b\tc\nd", wantChars: 4, wantPages: 1},
		"正常系_改ページを含む場合_改ページの数だけページを数えること": {text: "abc\fdef\f", wantChars: 6, wantPages: 2},
		"正常系_日本語を含む場合_1 文字として数えること":       {text: "論文\f", wantChars: 2, wantPages: 1},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			chars, pages := countText(tt.text)
			if chars != tt.wantChars {
				t.Errorf("chars = %d, want %d", chars, tt.wantChars)
			}
			if pages != tt.wantPages {
				t.Errorf("pages = %d, want %d", pages, tt.wantPages)
			}
		})
	}
}

func TestHasTextLayer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		chars int
		pages int
		want  bool
	}{
		"正常系_テキストが 1 文字もない場合_テキストレイヤーなしと判定されること":    {chars: 0, pages: 10, want: false},
		"正常系_スタンプ程度のノイズしかない場合_テキストレイヤーなしと判定されること":   {chars: 30, pages: 10, want: false},
		"正常系_本文相当の文字数がある場合_テキストレイヤーありと判定されること":      {chars: 20000, pages: 10, want: true},
		"境界値_1 ページあたりが閾値ちょうどの場合_テキストレイヤーありと判定されること": {chars: 500, pages: 10, want: true},
		"境界値_ページ数がゼロの場合_ゼロ除算せず false になること":         {chars: 100, pages: 0, want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := hasTextLayer(tt.chars, tt.pages, DefaultMinCharsPerPage); got != tt.want {
				t.Errorf("hasTextLayer(%d, %d) = %v, want %v", tt.chars, tt.pages, got, tt.want)
			}
		})
	}
}

func TestExtractTextWithTextLayer(t *testing.T) {
	r := requirePoppler(t)

	lines := []string{
		"Structured extraction of scholarly documents",
		"Abstract This paper evaluates two extraction routes",
		"and compares their fidelity against the source text.",
	}
	tmp := t.TempDir()
	path := writeTestPDF(t, filepath.Join(tmp, "sample.pdf"), []string{
		textPageContent(lines),
		textPageContent(lines),
	})
	outPath := filepath.Join(tmp, "text", "layer.txt")

	got, err := r.ExtractText(context.Background(), path, outPath)
	if err != nil {
		t.Fatalf("ExtractText() error = %v", err)
	}

	if got.Path != outPath {
		t.Errorf("Path = %q, want %q", got.Path, outPath)
	}
	if !got.HasTextLayer {
		t.Errorf("HasTextLayer = false, want true (chars=%d pages=%d)", got.Chars, got.Pages)
	}
	if got.Pages != 2 {
		t.Errorf("Pages = %d, want 2", got.Pages)
	}

	text, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read extracted text: %v", err)
	}
	if !strings.Contains(string(text), "Abstract") {
		t.Errorf("抽出テキストに本文が含まれない: %q", string(text))
	}
}

func TestExtractTextWithoutTextLayer(t *testing.T) {
	r := requirePoppler(t)

	tmp := t.TempDir()
	path := writeTestPDF(t, filepath.Join(tmp, "scan.pdf"), []string{
		blankPageContent(),
		blankPageContent(),
	})
	outPath := filepath.Join(tmp, "text", "layer.txt")

	got, err := r.ExtractText(context.Background(), path, outPath)
	if err != nil {
		t.Fatalf("ExtractText() error = %v", err)
	}

	if got.HasTextLayer {
		t.Errorf("HasTextLayer = true, want false (chars=%d pages=%d)", got.Chars, got.Pages)
	}
	if got.Chars != 0 {
		t.Errorf("Chars = %d, want 0", got.Chars)
	}
}
