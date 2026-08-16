package preprocess

import (
	"strings"
	"testing"

	"github.com/tamaco489/folio/backend/internal/domain"
)

func TestDetectLanguage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		text         string
		hasTextLayer bool
		wantLanguage domain.Language
		wantDetected bool
	}{
		"正常系_本文が日本語の場合_ja と判定されること": {
			text:         strings.Repeat("本論文では文書の構造化を扱う。", 20),
			hasTextLayer: true,
			wantLanguage: domain.LanguageJapanese,
			wantDetected: true,
		},
		"正常系_本文が英語の場合_en と判定されること": {
			text:         strings.Repeat("This paper evaluates two extraction routes. ", 10),
			hasTextLayer: true,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: true,
		},
		"正常系_英文要旨を含む日本語論文の場合_ja と判定されること": {
			text: "Abstract This paper evaluates two extraction routes for scholarly documents. " +
				strings.Repeat("本論文では論文の構造化抽出を二つの経路で比較する。", 20),
			hasTextLayer: true,
			wantLanguage: domain.LanguageJapanese,
			wantDetected: true,
		},
		"正常系_日本語の用例を含む英語論文の場合_en と判定されること": {
			text: strings.Repeat("We evaluate the extraction pipeline on scholarly documents. ", 40) +
				strings.Repeat("入力例", 30),
			hasTextLayer: true,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: true,
		},
		"境界値_日本語文字の比率が閾値ちょうどの場合_ja と判定されること": {
			text:         strings.Repeat("あ", 20) + strings.Repeat("a", 180),
			hasTextLayer: true,
			wantLanguage: domain.LanguageJapanese,
			wantDetected: true,
		},
		"境界値_日本語文字の比率が閾値をわずかに下回る場合_en と判定されること": {
			text:         strings.Repeat("あ", 19) + strings.Repeat("a", 181),
			hasTextLayer: true,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: true,
		},
		"境界値_判定に足りる文字数がない場合_根拠なしで en になること": {
			text:         strings.Repeat("あ", MinLettersForDetection-1),
			hasTextLayer: true,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: false,
		},
		"境界値_空文字の場合_根拠なしで en になること": {
			text:         "",
			hasTextLayer: true,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: false,
		},
		"境界値_数字と記号しかない場合_根拠なしで en になること": {
			text:         strings.Repeat("1234567890 (+-=) [1] 3.14 ", 20),
			hasTextLayer: true,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: false,
		},
		"異常系_テキストレイヤーがない場合_日本語が混じっていても根拠なしで en になること": {
			text:         strings.Repeat("本論文では文書の構造化を扱う。", 20),
			hasTextLayer: false,
			wantLanguage: domain.LanguageEnglish,
			wantDetected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			language, detected := DetectLanguage(tt.text, tt.hasTextLayer)
			if language != tt.wantLanguage {
				t.Errorf("language = %q, want %q", language, tt.wantLanguage)
			}
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v", detected, tt.wantDetected)
			}
		})
	}
}

func TestCountLetters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		text         string
		wantJapanese int
		wantLatin    int
	}{
		"正常系_ひらがなの場合_日本語として数えること":     {text: "あいうえお", wantJapanese: 5},
		"正常系_カタカナの場合_日本語として数えること":     {text: "カタカナ", wantJapanese: 4},
		"正常系_漢字の場合_日本語として数えること":       {text: "論文抽出", wantJapanese: 4},
		"正常系_ラテン文字の場合_英語として数えること":     {text: "Abstract", wantLatin: 8},
		"正常系_日本語と英語が混在する場合_双方を数えること":  {text: "論文 PDF の構造化", wantJapanese: 6, wantLatin: 3},
		"境界値_数字と記号と空白の場合_どちらにも数えないこと": {text: "3.14 [1] (+-=)\n\t", wantJapanese: 0, wantLatin: 0},
		"境界値_句読点の場合_どちらにも数えないこと":      {text: "、。「」・ー", wantJapanese: 0, wantLatin: 0},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			japanese, latin := countLetters(tt.text)
			if japanese != tt.wantJapanese {
				t.Errorf("japanese = %d, want %d", japanese, tt.wantJapanese)
			}
			if latin != tt.wantLatin {
				t.Errorf("latin = %d, want %d", latin, tt.wantLatin)
			}
		})
	}
}
