package preprocess

import (
	"unicode"

	"github.com/tamaco489/folio/backend/internal/domain"
)

const (
	// JapaneseRatioThreshold は日本語と判定する日本語文字の下限比率
	//
	// 分母は日本語文字とラテン文字の合計で、数字・記号・空白は数えない (数式や表が多い論文で比率が歪むのを避けるため)
	//   - 日本語論文は英文要旨と英語文献を含んでも本文が日本語であり、比率は 5 割を超える
	//   - 日本語の用例を載せた英語論文は、用例が数千字でも本文 3 万字に対して 1 割に届かない
	//
	// 双方に余裕のある 10% を境界とする
	JapaneseRatioThreshold = 0.10

	// MinLettersForDetection は判定に必要な文字数の下限
	//
	// ページ番号やスタンプ由来の数文字から比率を求めると、わずかな混入で判定が反転する
	MinLettersForDetection = 100
)

// DetectLanguage は抽出したテキストレイヤーの文字種から言語を判定する
//
// 2 つ目の返り値は判定の根拠が得られたかを表す
// テキストレイヤーを持たない PDF は判定材料がないため、既定の en を返しつつ false を添える
// 経路 B のモデルに判定させる案は前処理の責務を広げるため Phase 1 では採らない
func DetectLanguage(text string, hasTextLayer bool) (domain.Language, bool) {
	if !hasTextLayer {
		return domain.LanguageEnglish, false
	}

	japanese, latin := countLetters(text)
	total := japanese + latin
	if total < MinLettersForDetection {
		return domain.LanguageEnglish, false
	}

	if float64(japanese)/float64(total) >= JapaneseRatioThreshold {
		return domain.LanguageJapanese, true
	}
	return domain.LanguageEnglish, true
}

// countLetters は日本語文字とラテン文字を数える
//
// Han は中国語も含むが、判定は ja と en の二択であるため区別しない
func countLetters(text string) (japanese, latin int) {
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r),
			unicode.Is(unicode.Han, r):
			japanese++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	return japanese, latin
}
