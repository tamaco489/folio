package normalize

import (
	"testing"

	"github.com/tamaco489/folio/backend/internal/domain"
)

func TestHeadingLevel(t *testing.T) {
	tests := map[string]struct {
		heading  string
		level    int
		want     int
		wantWarn bool
	}{
		"正常系_アラビア数字 2 要素の場合_要素数の 2 になること":     {heading: "3.1 Routing Function", level: 1, want: 2},
		"正常系_末尾にピリオドが付く場合_1 になること":            {heading: "1. Introduction", level: 3, want: 1},
		"正常系_3 要素に末尾ピリオドが付く場合_3 になること":        {heading: "2.3.1. Details", level: 1, want: 3},
		"正常系_英字と数字の 2 要素の場合_2 になること":          {heading: "A.2 Proofs", level: 1, want: 2},
		"正常系_英字 1 字の付録の場合_1 になること":            {heading: "A Additional Results", level: 2, want: 1},
		"正常系_ローマ数字と数字の 2 要素の場合_2 になること":       {heading: "II.3 Setup", level: 1, want: 2},
		"正常系_ローマ数字にピリオドが付く場合_1 になること":         {heading: "IV. Experiments", level: 2, want: 1},
		"正常系_日本語の番号付き見出しの場合_番号から決まること":        {heading: "1 はじめに", level: 2, want: 1},
		"正常系_番号のない見出しの場合_経路のレベルを保つこと":         {heading: "Abstract", level: 2, want: 2},
		"正常系_年で始まる見出しの場合_番号とみなさず経路のレベルを保つこと":  {heading: "2019 Shared Task Overview", level: 2, want: 2},
		"正常系_数字に英字が続く場合_番号とみなさず経路のレベルを保つこと":   {heading: "3D Reconstruction", level: 2, want: 2},
		"正常系_番号の後に空白がない場合_番号とみなさず経路のレベルを保つこと": {heading: "1.Introduction", level: 2, want: 2},
		"境界値_番号なしでレベルが 0 の場合_1 に丸めて警告すること":    {heading: "Introduction", level: 0, want: 1, wantWarn: true},
		"境界値_番号なしでレベルが上限を超える場合_上限に丸めて警告すること":  {heading: "Related Work", level: 9, want: maxHeadingLevel, wantWarn: true},
		"境界値_番号が上限を超える要素数の場合_上限に丸めて警告すること":    {heading: "1.2.3.4.5 Deep", level: 1, want: maxHeadingLevel, wantWarn: true},
		"境界値_見出しが空でレベルが 0 の場合_1 に丸めて警告すること":   {heading: "", level: 0, want: 1, wantWarn: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := Normalize(domain.Document{
				Sections: []domain.Section{{Level: tt.level, Heading: tt.heading}},
			}, WithClock(fixedClock))

			if got.Sections[0].Level != tt.want {
				t.Errorf("Level = %d, want %d", got.Sections[0].Level, tt.want)
			}
			if hasWarning(got, "sections[0] の見出しレベル") != tt.wantWarn {
				t.Errorf("見出しレベルの警告の有無 = %v, want %v\n%v", !tt.wantWarn, tt.wantWarn, got.Provenance.Warnings)
			}
		})
	}
}

// 見出しの空白を揃えてから番号を読むため、番号と題名の間の改行や連続空白があっても番号として扱えること
func TestHeadingLevelAfterCollapse(t *testing.T) {
	got := Normalize(domain.Document{
		Sections: []domain.Section{{Level: 1, Heading: "  3.1 \n  Routing   Function "}},
	}, WithClock(fixedClock))

	if got.Sections[0].Heading != "3.1 Routing Function" {
		t.Errorf("Heading = %q", got.Sections[0].Heading)
	}
	if got.Sections[0].Level != 2 {
		t.Errorf("Level = %d, want 2", got.Sections[0].Level)
	}
}
