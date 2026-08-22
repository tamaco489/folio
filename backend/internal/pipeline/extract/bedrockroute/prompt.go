package bedrockroute

import (
	"fmt"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// systemPrompt は 1 ページ分の構造化を指示する
//
// 指示を英語で書くのは、日本語で書くと出力言語が日本語へ引きずられ英語論文の本文が訳出される事故が起きうるため
// 転記は原本の言語に従い翻訳しないことを明示しており、日本語のページでも指示が英語であることは出力に影響しない
//
// 出力の形は pageTool のスキーマで渡すため、ここには各キーの意味とルールだけを書く
const systemPrompt = `You extract structured data from a single page image of an academic paper.

Record the result by calling the extract_page tool. Its input schema defines the keys.

Rules:
- Transcribe text in the language of the document. Never translate.
- Never invent a value. Omit an optional key you cannot fill. A required key with nothing to record takes "" or [].
- "title", "authors", "abstract" and "keywords" describe the paper as a whole. Fill them only on the page that prints them, which is normally the first page, and omit them elsewhere.
- "sections" lists the body blocks of this page in reading order. Start a new entry at every heading, put the heading text in "heading" and its depth in "level", where a top level heading is 1.
- If the page opens with body text that carries no heading, make it the first entry and omit "heading".
- "continuesPreviousSection" is true when that opening body text carries on the section from the previous page, and false when it starts new content whose heading is not printed on this page. Omit the key when the page opens with a heading.
- "sections[].text" holds running text only. Leave out captions, running headers, page numbers and footnotes.
- "figures" and "tables" put the printed label such as "Figure 1" or "図 1" in "label" and the caption sentence in "caption".
- "tables[].header" is the array of header rows, so a two level header has two entries. Repeat a merged cell value into every cell it spans.
- "references" lists the bibliography entries printed on this page, and "raw" is the entry exactly as printed.
- "continuesPreviousReference" is true when the first entry of "references" is the tail of an entry that began on the previous page. Omit the key otherwise.`

// pagePrompt はページ画像に添える指示を組み立てる
func pagePrompt(page int, lang domain.Language) string {
	s := fmt.Sprintf("This image is page %d of the document. Extract it with the extract_page tool.", page)
	if lang != "" {
		s += fmt.Sprintf(" The document is written in %q (ISO 639-1), so transcribe it in that language.", string(lang))
	}
	return s
}
