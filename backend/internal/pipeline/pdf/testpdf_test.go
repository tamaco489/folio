package pdf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// テスト用の PDF は実行時に自前で生成する
//
// arXiv 由来の PDF は再配布不可のため testdata に置けず、依存追加も禁止されているため最小限の PDF を組み立てる

// textPageContent はテキストレイヤーを持つページの内容ストリームを組み立てる
func textPageContent(lines []string) string {
	var b strings.Builder
	b.WriteString("BT\n/F1 12 Tf\n72 780 Td\n")
	for _, line := range lines {
		fmt.Fprintf(&b, "(%s) Tj\n0 -16 Td\n", escapePDFString(line))
	}
	b.WriteString("ET\n")
	return b.String()
}

// blankPageContent は塗り潰した矩形のみを描くページの内容ストリームを組み立てる
//
// テキストを一切含まないためスキャン画像の PDF を模せる
func blankPageContent() string {
	return "0.5 0.5 0.5 rg\n72 500 450 250 re\nf\n"
}

// escapePDFString は PDF の文字列リテラルで意味を持つ文字を退避する
func escapePDFString(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return replacer.Replace(s)
}

// writeTestPDF は各ページの内容ストリームから PDF を生成しファイルに書き出す
func writeTestPDF(t *testing.T, path string, contents []string) string {
	t.Helper()

	if len(contents) == 0 {
		t.Fatal("writeTestPDF: contents is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("writeTestPDF: mkdir: %v", err)
	}
	if err := os.WriteFile(path, buildTestPDF(contents), 0o644); err != nil {
		t.Fatalf("writeTestPDF: write: %v", err)
	}
	return path
}

// buildTestPDF は非圧縮の PDF バイト列を組み立てる
//
// オブジェクト番号は 1=Catalog, 2=Pages, 以降ページごとに (2i+1)=Page, (2i+2)=Contents, 最後にフォントを置く
func buildTestPDF(contents []string) []byte {
	pageCount := len(contents)
	fontNum := 2*pageCount + 3

	objects := make([]string, 0, fontNum)

	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")

	kids := make([]string, 0, pageCount)
	for i := 1; i <= pageCount; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 2*i+1))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), pageCount))

	for i, content := range contents {
		pageNum := 2*(i+1) + 1
		contentNum := pageNum + 1
		// MediaBox は A4 (595x842pt)
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] "+
				"/Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			fontNum, contentNum))
		objects = append(objects, fmt.Sprintf(
			"<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}

	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")

	offsets := make([]int, len(objects)+1)
	for i, obj := range objects {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}

	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, xrefOffset)

	return buf.Bytes()
}
