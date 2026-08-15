package pdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

// pagePrefix は pdftoppm に渡す出力ファイル名の接頭辞
const pagePrefix = "page"

// pageFilePattern は pdftoppm が出力するファイル名にマッチする
//
// pdftoppm のゼロ埋め桁数は総ページ数に依存するため、桁数を固定せずに拾う
var pageFilePattern = regexp.MustCompile(`^` + pagePrefix + `-(\d+)\.png$`)

// RasterizeResult はラスタライズの結果
type RasterizeResult struct {
	// Dir はページ画像を書き出したディレクトリ
	Dir string

	// Pages はページ画像の絶対パス (ページ昇順)
	Pages []string

	// DPI は描画に使用した解像度
	DPI int
}

// PageFileName はページ番号から画像ファイル名を導出する
//
// Textract の非同期 API は 3,000 ページまで扱えるため 4 桁ゼロ埋めとする
// 3 桁だと 999 ページを超えた時点で桁が増え、辞書順とページ順がずれる
// (page-1000 が page-999 より前に並ぶ)
func PageFileName(page int) string {
	return fmt.Sprintf("%s-%04d.png", pagePrefix, page)
}

// Rasterize は PDF の全ページを PNG に変換する
func (r *Runner) Rasterize(ctx context.Context, pdfPath, outDir string) (RasterizeResult, error) {
	return r.RasterizePages(ctx, pdfPath, outDir, 0, 0)
}

// RasterizePages は指定範囲のページを PNG に変換する
//
// first, last に 0 を渡すとそれぞれ先頭ページ、末尾ページとして扱う
// Lambda の /tmp は容量に上限があるため、ページ数が多い PDF は
// 呼び出し側で範囲を分割して複数回に分けられるようにしてある
func (r *Runner) RasterizePages(ctx context.Context, pdfPath, outDir string, first, last int) (RasterizeResult, error) {
	if first < 0 || last < 0 || (last != 0 && first != 0 && last < first) {
		return RasterizeResult{}, fmt.Errorf("pdf: invalid page range: first=%d last=%d", first, last)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return RasterizeResult{}, fmt.Errorf("pdf: create output dir: %w", err)
	}

	args := []string{"-png", "-r", strconv.Itoa(r.dpi)}
	if first > 0 {
		args = append(args, "-f", strconv.Itoa(first))
	}
	if last > 0 {
		args = append(args, "-l", strconv.Itoa(last))
	}
	args = append(args, pdfPath, filepath.Join(outDir, pagePrefix))

	if _, err := r.run(ctx, binPDFToPPM, args...); err != nil {
		return RasterizeResult{}, err
	}

	pages, err := normalizePageFiles(outDir)
	if err != nil {
		return RasterizeResult{}, err
	}
	if len(pages) == 0 {
		return RasterizeResult{}, ErrNoPagesRendered
	}

	return RasterizeResult{Dir: outDir, Pages: pages, DPI: r.dpi}, nil
}

// normalizePageFiles は pdftoppm の出力を 4 桁ゼロ埋めの名前に揃える
//
// pdftoppm はゼロ埋め桁数を総ページ数から決めるため、10 ページの PDF では
// page-01.png になる
// 後続の Lambda が桁数を意識せずに済むよう、ここで命名規約に揃える
func normalizePageFiles(outDir string) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("pdf: read output dir: %w", err)
	}

	numbers := make([]int, 0, len(entries))
	byNumber := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := pageFilePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		page, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		numbers = append(numbers, page)
		byNumber[page] = entry.Name()
	}
	sort.Ints(numbers)

	pages := make([]string, 0, len(numbers))
	for _, page := range numbers {
		src := filepath.Join(outDir, byNumber[page])
		dst := filepath.Join(outDir, PageFileName(page))
		if src != dst {
			if err := os.Rename(src, dst); err != nil {
				return nil, fmt.Errorf("pdf: rename %s: %w", src, err)
			}
		}
		pages = append(pages, dst)
	}
	return pages, nil
}
