package bedrockroute

import "github.com/tamaco489/folio/backend/internal/awsx/bedrock"

// pageTool は 1 ページ分の結果を tool use の入力として受け取る定義
//
// 自由文の JSON ではモデルが本文中の引用符をエスケープせず PageDecodeError になったため、スキーマを Bedrock に渡して整形を保証させる
//
// キー名は PageResult の json タグと一致させる — 揃えられない箇所 (figures の label など) は Merge で domain の型へ写す
// 値の意味 (転記の言語、continues* の判断基準など) は systemPrompt のルールで伝え、スキーマは型だけを持つ
var pageTool = &bedrock.ToolSpec{
	Name:        "extract_page",
	Description: "Record the structured content of one page of an academic paper.",
	Schema: object(map[string]any{
		"title":    str,
		"authors":  array(object(map[string]any{"name": str, "affiliation": str, "email": str})),
		"abstract": str,
		"keywords": array(str),
		"sections": array(object(map[string]any{"level": integer, "heading": str, "text": str})),
		"figures":  array(object(map[string]any{"label": str, "caption": str})),
		"tables": array(object(map[string]any{
			"label":   str,
			"caption": str,
			"header":  array(array(str)),
			"rows":    array(array(str)),
		})),
		"references": array(object(map[string]any{
			"raw":     str,
			"title":   str,
			"authors": array(str),
			"year":    integer,
			"venue":   str,
			"doi":     str,
		})),
		"continuesPreviousSection":   boolean,
		"continuesPreviousReference": boolean,
	}),
}

var (
	str     = map[string]any{"type": "string"}
	integer = map[string]any{"type": "integer"}
	boolean = map[string]any{"type": "boolean"}
)

func object(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties}
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
