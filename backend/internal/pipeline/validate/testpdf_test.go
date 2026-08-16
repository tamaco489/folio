package validate

import (
	"bytes"
	"fmt"
	"strings"
)

// buildPDF は pdfinfo が読める非圧縮の PDF バイト列を組み立てる
//
// arXiv 由来の PDF は再配布不可で testdata に置けないため、テスト用の入力は実行時に生成する
// オブジェクト番号は 1=Catalog, 2=Pages, 以降ページごとに (2i+1)=Page, (2i+2)=Contents, 最後にフォントを置く
func buildPDF(pages int) []byte {
	fontNum := 2*pages + 3
	objects := make([]string, 0, fontNum)

	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")

	kids := make([]string, 0, pages)
	for i := 1; i <= pages; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 2*i+1))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), pages))

	// 塗り潰した矩形のみを描く (テキストレイヤーの有無はこのパッケージの判定に関わらない)
	const content = "0.5 0.5 0.5 rg\n72 500 450 250 re\nf\n"
	for i := range pages {
		pageNum := 2*(i+1) + 1
		// MediaBox は A4 (595x842pt)
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] "+
				"/Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			fontNum, pageNum+1))
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
