package pdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unicode"
)

// pageBreak は pdftotext がページ区切りに挿入する改ページ文字
const pageBreak = '\f'

// TextResult はテキストレイヤー抽出の結果
type TextResult struct {
	// Path は抽出したテキストの書き出し先
	Path string

	// Chars は空白を除いた文字数
	Chars int

	// Pages は抽出できたページ数
	Pages int

	// HasTextLayer はテキストレイヤーを持つと判定したか
	HasTextLayer bool
}

// ExtractText は PDF のテキストレイヤーを抽出する
//
// -layout は付けない
// 二段組の論文で物理レイアウトを保つと左右の段が行単位で混ざり、
// 検証層 (#23) が抽出値を原文と突合する際の部分一致が壊れるため、
// 読み順を優先する既定の動作を使う
func (r *Runner) ExtractText(ctx context.Context, pdfPath, outPath string) (TextResult, error) {
	if dir := filepath.Dir(outPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return TextResult{}, fmt.Errorf("pdf: create output dir: %w", err)
		}
	}

	// -eol unix で改行を LF に固定し、実行環境による差分をなくす
	args := []string{"-enc", "UTF-8", "-eol", "unix", pdfPath, outPath}
	if _, err := r.run(ctx, binPDFToText, args...); err != nil {
		return TextResult{}, err
	}

	text, err := os.ReadFile(outPath)
	if err != nil {
		return TextResult{}, fmt.Errorf("pdf: read extracted text: %w", err)
	}

	chars, pages := countText(string(text))
	return TextResult{
		Path:         outPath,
		Chars:        chars,
		Pages:        pages,
		HasTextLayer: hasTextLayer(chars, pages, r.minCharsPerPage),
	}, nil
}

// countText は空白を除いた文字数とページ数を数える
//
// ページ数は pdftotext が各ページ末尾に置く改ページ文字から求める
// pdfinfo をもう一度起動せずに済ませるための措置
func countText(text string) (chars, pages int) {
	for _, r := range text {
		switch {
		case r == pageBreak:
			pages++
		case unicode.IsSpace(r):
		default:
			chars++
		}
	}
	if pages == 0 {
		pages = 1
	}
	return chars, pages
}

// hasTextLayer は 1 ページあたりの文字数が閾値以上かを判定する
func hasTextLayer(chars, pages, minCharsPerPage int) bool {
	if pages <= 0 {
		return false
	}
	return chars/pages >= minCharsPerPage
}
