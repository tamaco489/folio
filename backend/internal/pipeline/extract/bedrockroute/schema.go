package bedrockroute

import "github.com/tamaco489/folio/backend/internal/awsx/bedrock"

// pageTool は 1 ページ分の結果を tool use の入力として受け取る定義
//
// 自由文の JSON ではモデルが本文中の引用符をエスケープせず PageDecodeError になったため、スキーマを Bedrock に渡して整形を保証させる
//
// キー名は PageResult の json タグと一致させる — 揃えられない箇所 (figures の label など) は Merge で domain の型へ写す
// 値の意味 (転記の言語、continues* の判断基準など) は systemPrompt のルールで伝え、スキーマは型だけを持つ
//
// required は常に値のあるキーに限る — 配列は該当が無くても [] で返させ、書誌情報や doi のように無いことが多いキーは省略可にして空文字で埋めさせない
var pageTool = &bedrock.ToolSpec{
	Name:        "extract_page",
	Description: "Record the structured content of one page of an academic paper.",
	Schema: object(map[string]any{
		"title":    str,
		"authors":  array(object(map[string]any{"name": str, "affiliation": str, "email": str}, "name")),
		"abstract": str,
		"keywords": array(str),
		"sections": array(object(map[string]any{"level": integer, "heading": str, "text": str}, "text")),
		"figures":  array(object(map[string]any{"label": str, "caption": str}, "label", "caption")),
		"tables": array(object(map[string]any{
			"label":   str,
			"caption": str,
			"header":  array(array(str)),
			"rows":    array(array(str)),
		}, "label", "caption", "header", "rows")),
		"references": array(object(map[string]any{
			"raw":     str,
			"title":   str,
			"authors": array(str),
			"year":    integer,
			"venue":   str,
			"doi":     str,
		}, "raw")),
		"continuesPreviousSection":   boolean,
		"continuesPreviousReference": boolean,
	}, "sections", "figures", "tables", "references"),
}

var (
	str     = map[string]any{"type": "string"}
	integer = map[string]any{"type": "integer"}
	boolean = map[string]any{"type": "boolean"}
)

// object は strict な tool use の object を組み立てる (additionalProperties: false は strict の必須条件)
func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             append([]string{}, required...),
		"additionalProperties": false,
	}
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
