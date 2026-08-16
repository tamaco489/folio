package verify

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ligatures は PDF のテキストレイヤーに残りやすい Latin 合字 (U+FB00 - U+FB06) の展開表
//
// golang.org/x/text が go.mod に無く NFKC を使えないため、突合で実際に効く範囲だけを自前で畳む
var ligatures = map[rune]string{
	'ﬀ': "ff",
	'ﬁ': "fi",
	'ﬂ': "fl",
	'ﬃ': "ffi",
	'ﬄ': "ffl",
	'ﬅ': "ft",
	'ﬆ': "st",
}

// hyphenation は行末ハイフネーション (ハイフンの直後の改行)
//
// U+00AD (soft hyphen) と U+2010 (hyphen) も pdftotext の出力に現れうるため含める
var hyphenation = regexp.MustCompile(`[-\x{00AD}\x{2010}][ \t]*\n[ \t]*`)

// expandLigatures は合字を構成文字へ展開する (strings.ToLower は合字を畳まないため、小文字化より前に掛ける)
func expandLigatures(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { _, ok := ligatures[r]; return ok }) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if exp, ok := ligatures[r]; ok {
			sb.WriteString(exp)
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// fold は部分文字列一致のために表記ゆれを畳む
//
// 合字を展開して小文字化し、字母と数字以外 (空白、句読点、引用符、ダッシュ類、結合文字) をすべて除く
// 空白とハイフンを残さないため、行末ハイフネーション "long-\ncontext" と "long-context" はどちらも "longcontext" になり、結合の処理を別に持たなくてよい
// 引用符・ダッシュの種類の違いも同様に消える
func fold(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range strings.ToLower(expandLigatures(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// wordScript は空白で単語を区切る文字種かを返す
//
// 空白の無い文字種 (漢字、かな、ハングルなど) は単語境界が取れないため文字バイグラムに落とす
func wordScript(r rune) bool {
	if r < utf8.RuneSelf {
		return true
	}
	return unicode.IsDigit(r) || unicode.In(r, unicode.Latin, unicode.Greek, unicode.Cyrillic)
}

// tokenize は一致度の計算に用いるトークン列を返す
//
// 合字を展開して小文字化し、字母と数字以外を区切りとする
// 空白で単語を区切る文字種の連続は 1 語、それ以外の文字種の連続は文字バイグラム (1 文字なら 1 文字) にする
// 結合文字 (Mn) は区切りにせず読み飛ばす (合成済みと分解済みの表記を同じ語にするため)
func tokenize(s string) []string {
	var tokens []string
	var word, cjk []rune

	flush := func() {
		if len(word) > 0 {
			tokens = append(tokens, string(word))
			word = word[:0]
		}
		if len(cjk) == 1 {
			tokens = append(tokens, string(cjk))
		}
		for i := 0; i+1 < len(cjk); i++ {
			tokens = append(tokens, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}

	for _, r := range strings.ToLower(expandLigatures(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case wordScript(r):
			if len(cjk) > 0 {
				flush()
			}
			word = append(word, r)
		default:
			if len(word) > 0 {
				flush()
			}
			cjk = append(cjk, r)
		}
	}
	flush()
	return tokens
}

// pair は隣接する 2 トークンの組
//
// 順序を持たない (辞書順に並べる) ため、"Tanaka, Aiko" と "Aiko Tanaka" のような語順の入れ替わりを同じ組として数える
type pair [2]string

func newPair(a, b string) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a, b}
}

// corpus は原本テキストを突合しやすい形に索引化したもの
type corpus struct {
	folded string
	tokens map[string]struct{}
	pairs  map[pair]struct{}
}

// newCorpus は原本テキストを索引化する
//
// トークンは行末ハイフネーションを結合した版と結合しない版の両方から取る
// "trans-\nformer" は結合して "transformer" に、"long-\ncontext" は結合せず "long" "context" になるのが正しく、原本だけからはどちらか決められないため両方を持つ (集合に余分な語が増える害は小さい)
func newCorpus(text string) *corpus {
	c := &corpus{
		folded: fold(text),
		tokens: map[string]struct{}{},
		pairs:  map[pair]struct{}{},
	}
	c.add(text)
	if joined := hyphenation.ReplaceAllString(text, ""); joined != text {
		c.add(joined)
	}
	return c
}

func (c *corpus) add(text string) {
	tokens := tokenize(text)
	for i, t := range tokens {
		c.tokens[t] = struct{}{}
		if i > 0 {
			c.pairs[newPair(tokens[i-1], t)] = struct{}{}
		}
	}
}

// contains は表記ゆれを畳んだ上で s が原本に連続して現れるかを返す
func (c *corpus) contains(s string) bool {
	f := fold(s)
	return f != "" && strings.Contains(c.folded, f)
}

// coverage は s の隣接トークン対のうち原本に現れる割合を返す (トークンが 1 つなら語そのものの有無)
//
// 語順の入れ替わりや読み順の違いには対に分けることで耐え、原本の語彙で書かれた別の文 (要約や言い換え) は対が崩れるため見分けられる
// s にトークンが無ければ ok=false を返し、呼び出し側は根拠なしとして扱う
func (c *corpus) coverage(s string) (score float64, ok bool) {
	tokens := tokenize(s)
	switch len(tokens) {
	case 0:
		return 0, false
	case 1:
		if _, found := c.tokens[tokens[0]]; found {
			return 1, true
		}
		return 0, true
	}
	seen := make(map[pair]struct{}, len(tokens))
	hit := 0
	for i := 1; i < len(tokens); i++ {
		p := newPair(tokens[i-1], tokens[i])
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if _, found := c.pairs[p]; found {
			hit++
		}
	}
	return float64(hit) / float64(len(seen)), true
}

// shortScore は題名や著者名のような短い値の一致度を返す
//
// 畳んだ上で連続して現れれば 1.0 とし、現れなければトークン対の被覆率に落とす
// 部分文字列一致を先に見るのは、原本側で語が繋がって取れる場合 ("AikoTanaka1" のような上付き文字の混入) にトークンが崩れても救うため
func (c *corpus) shortScore(s string) (score float64, ok bool) {
	if c.contains(s) {
		return 1, true
	}
	return c.coverage(s)
}
