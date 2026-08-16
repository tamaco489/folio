package normalize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// collapseSpace は 1 行に収まるべき文字列の空白を揃える
//
// 前後の空白を除き、連続する空白は 1 つの空白にする
// 改行を含む空白列は原本の行折り返しに由来するため、両側が ASCII でなければ空白を挟まず連結する
// 日本語の文中に原本にない空白を入れないためで、bedrockroute.joinFragments と同じ規則
//
// 空白の判定は unicode.IsSpace (strings.TrimSpace や strings.Fields と同じ) に揃え、全角スペース (U+3000) も空白として扱う
// 文字種は変換しない方針だが、空白は内容ではなく配置の情報であり、経路が半角と全角のどちらを出力したかを比較しても意味がない
func collapseSpace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))

	var last rune
	pending, lineBreak := false, false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			pending = true
			lineBreak = lineBreak || r == '\n' || r == '\r'
			continue
		}
		if pending {
			if !lineBreak || last < utf8.RuneSelf || r < utf8.RuneSelf {
				sb.WriteByte(' ')
			}
			pending, lineBreak = false, false
		}
		sb.WriteRune(r)
		last = r
	}
	return sb.String()
}

// normalizeText は段落の情報を持ちうる本文の空白を揃える
//
// 改行は段落の区切りを表しうるため潰さず、改行コードの統一と行末の空白の除去、前後の空行の除去に留める
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// collapseStrings は文字列の配列の各要素の空白を揃え、空になった要素を除く
//
// omitempty の付くフィールドに使うため nil はそのまま返す
func collapseStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s = collapseSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// dedupe は文字列の配列から重複を除く (先着を残し、順序は保つ)
func dedupe(ss []string) []string {
	if ss == nil {
		return nil
	}
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
