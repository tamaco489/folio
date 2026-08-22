package textractroute

import (
	"maps"
	"slices"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
)

// paperTool は構造化の結果を tool use の入力として受け取る定義
//
// 自由文の JSON では見出しや参考文献に含まれる引用符をモデルがエスケープせず復号に失敗しうるため、スキーマを Bedrock に渡して整形を保証させる
//
// キー名は structured の json タグと一致させる
// raw はモデルに書かせない方針のためスキーマに含めない (wireReference.Raw は互換のために受けるだけ)
// from / to / element の意味と欠損時の値は systemPrompt のルールで伝え、スキーマは型だけを持つ
var paperTool = &bedrock.ToolSpec{
	Name:        "structure_paper",
	Description: "Record the bibliographic data, section outline and reference entries of an academic paper.",
	Schema: object(map[string]any{
		"title":    str,
		"authors":  array(object(map[string]any{"name": str, "affiliation": str, "email": str})),
		"abstract": str,
		"keywords": array(str),
		"venue":    str,
		"year":     integer,
		"sections": array(object(map[string]any{"level": integer, "heading": str, "from": integer, "to": integer})),
		"references": array(object(map[string]any{
			"element": integer,
			"title":   str,
			"authors": array(str),
			"year":    integer,
			"venue":   str,
			"doi":     nullableStr,
		})),
	}),
}

var (
	str         = map[string]any{"type": "string"}
	nullableStr = map[string]any{"type": []string{"string", "null"}}
	integer     = map[string]any{"type": "integer"}
)

// object は strict な tool use の object を組み立てる (additionalProperties: false は strict の必須条件)
//
// この経路は欠損を "" / [] / 0 / null で表す約束 (systemPrompt) のため、全キーを required にする
func object(properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             slices.Sorted(maps.Keys(properties)),
		"additionalProperties": false,
	}
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
