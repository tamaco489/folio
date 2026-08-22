package bedrock

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DecodeJSON は応答を構造化 JSON として v に読み込む
//
// 生成が上限で打ち切られた応答 (StopReason が max_tokens) は末尾が欠けているため、JSON の解釈に入らず ErrOutputTruncated を返す
//
// tool use で受け取った応答は ToolInput をそのまま解釈する
//
// ToolInput が無い応答は自由文として扱う (tool use 以前の記録を再生するために残す)
// モデルは前置きやコードフェンスを伴う応答を返すことがあるため、JSON 部分を切り出してから解釈する
//
// 解釈できない場合は ErrInvalidJSON を返し、生の応答は Response に残したままとする
func (r *Response) DecodeJSON(v any) error {
	if r.StopReason == StopReasonMaxTokens {
		return fmt.Errorf("%w (outputTokens=%d)", ErrOutputTruncated, r.Usage.OutputTokens)
	}
	if len(r.ToolInput) > 0 {
		if err := json.Unmarshal(r.ToolInput, v); err != nil {
			return fmt.Errorf("%w: tool input: %w", ErrInvalidJSON, err)
		}
		return nil
	}
	payload, ok := extractJSON(r.Text)
	if !ok {
		return fmt.Errorf("%w: no json object found", ErrInvalidJSON)
	}
	if err := json.Unmarshal([]byte(payload), v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	return nil
}

// extractJSON はコードフェンスや前後の散文を取り除いて JSON 部分を返す
func extractJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return s, true
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return "", false
	}
	closing := byte('}')
	if s[start] == '[' {
		closing = ']'
	}
	end := strings.LastIndexByte(s, closing)
	if end < start {
		return "", false
	}
	return s[start : end+1], true
}
