package normalize

import (
	"reflect"
	"testing"

	"github.com/tamaco489/folio/backend/internal/domain"
)

func TestNormalizeDOI(t *testing.T) {
	tests := map[string]struct {
		in       *string
		want     *string
		wantWarn bool
	}{
		"正常系_https の URL の場合_接頭辞を剥がして小文字になること":  {in: new("https://doi.org/10.48550/ARXIV.2309.17453"), want: new("10.48550/arxiv.2309.17453")},
		"正常系_dx.doi.org の URL の場合_接頭辞を剥がすこと":    {in: new("http://dx.doi.org/10.1000/xyz123"), want: new("10.1000/xyz123")},
		"正常系_doi: の接頭辞の場合_剥がすこと":                {in: new("doi:10.1000/xyz123"), want: new("10.1000/xyz123")},
		"正常系_大文字の DOI: と URL が重なる場合_両方剥がすこと":    {in: new("DOI: HTTPS://DOI.ORG/10.1000/XYZ123"), want: new("10.1000/xyz123")},
		"正常系_前後に空白がある場合_除かれること":                 {in: new("  10.1000/xyz123 \n"), want: new("10.1000/xyz123")},
		"正常系_既に正規形の場合_変わらないこと":                  {in: new("10.1000/xyz123"), want: new("10.1000/xyz123")},
		"正常系_nil の場合_nil のままで警告しないこと":           {in: nil, want: nil},
		"正常系_空文字の場合_未特定として nil にし警告しないこと":       {in: new("  "), want: nil},
		"異常系_10. で始まらない場合_nil に戻して警告すること":       {in: new("arXiv:2309.17453"), want: nil, wantWarn: true},
		"異常系_登録者番号の後にスラッシュがない場合_nil に戻して警告すること": {in: new("10.1000"), want: nil, wantWarn: true},
		"異常系_接頭辞だけの場合_nil に戻して警告すること":           {in: new("https://doi.org/"), want: nil, wantWarn: true},
		"異常系_接尾辞に空白を含む場合_nil に戻して警告すること":        {in: new("10.1000/xyz 123"), want: nil, wantWarn: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := Normalize(domain.Document{
				References: []domain.Reference{{Raw: "r", DOI: tt.in}},
			}, WithClock(fixedClock))

			if !reflect.DeepEqual(got.References[0].DOI, tt.want) {
				t.Errorf("DOI = %s, want %s", strPtr(got.References[0].DOI), strPtr(tt.want))
			}
			if hasWarning(got, "references[0] の doi") != tt.wantWarn {
				t.Errorf("DOI の警告の有無 = %v, want %v\n%v", !tt.wantWarn, tt.wantWarn, got.Provenance.Warnings)
			}
		})
	}
}

func TestNormalizeYear(t *testing.T) {
	tests := map[string]struct {
		in       int
		want     int
		wantWarn bool
	}{
		"正常系_範囲内の場合_変わらないこと":            {in: 2017, want: 2017},
		"正常系_未設定の場合_0 のままで警告しないこと":      {in: 0, want: 0},
		"境界値_下限の場合_変わらないこと":             {in: minYear, want: minYear},
		"境界値_現在の翌年の場合_変わらないこと":          {in: 2027, want: 2027},
		"境界値_下限の 1 つ下の場合_未設定に戻して警告すること": {in: minYear - 1, want: 0, wantWarn: true},
		"境界値_現在の 2 年後の場合_未設定に戻して警告すること": {in: 2028, want: 0, wantWarn: true},
		"異常系_桁が多い場合_未設定に戻して警告すること":      {in: 20017, want: 0, wantWarn: true},
		"異常系_負の場合_未設定に戻して警告すること":        {in: -2017, want: 0, wantWarn: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := Normalize(domain.Document{
				Metadata:   domain.Metadata{Year: tt.in},
				References: []domain.Reference{{Raw: "r", Year: tt.in}},
			}, WithClock(fixedClock))

			if got.Metadata.Year != tt.want {
				t.Errorf("Metadata.Year = %d, want %d", got.Metadata.Year, tt.want)
			}
			if got.References[0].Year != tt.want {
				t.Errorf("References[0].Year = %d, want %d", got.References[0].Year, tt.want)
			}
			if hasWarning(got, "metadata の year") != tt.wantWarn {
				t.Errorf("metadata の年の警告の有無 = %v, want %v\n%v", !tt.wantWarn, tt.wantWarn, got.Provenance.Warnings)
			}
			if hasWarning(got, "references[0] の year") != tt.wantWarn {
				t.Errorf("references の年の警告の有無 = %v, want %v\n%v", !tt.wantWarn, tt.wantWarn, got.Provenance.Warnings)
			}
		})
	}
}

func TestReferenceTitle(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"正常系_末尾にピリオドがある場合_1 つ除かれること":        {in: "Attention Is All You Need.", want: "Attention Is All You Need"},
		"正常系_ピリオドの前に空白がある場合_ピリオドと空白が除かれること": {in: "Attention Is All You Need .", want: "Attention Is All You Need"},
		"正常系_末尾にピリオドがない場合_変わらないこと":          {in: "Attention Is All You Need", want: "Attention Is All You Need"},
		"正常系_途中にピリオドがある場合_末尾だけ除かれること":       {in: "Learning to Rank for U.S. Courts.", want: "Learning to Rank for U.S. Courts"},
		"境界値_末尾が省略記号の場合_変わらないこと":            {in: "Ends with ellipsis...", want: "Ends with ellipsis..."},
		"境界値_ピリオドだけの場合_空文字になること":            {in: ".", want: ""},
		"境界値_空文字の場合_空文字のままであること":            {in: "", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := referenceTitle(tt.in); got != tt.want {
				t.Errorf("referenceTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// 参照文献の文字列は機械的なゆれだけを揃え、著者名の表記や並び順は変えないこと
func TestNormalizeReferenceText(t *testing.T) {
	got := Normalize(domain.Document{
		References: []domain.Reference{
			{
				Raw:     "Vaswani, A.,\n  Shazeer, N.  Attention Is All You Need.\nNeurIPS, 2017. ",
				Title:   " Attention Is All You Need. ",
				Authors: []string{"", " Ashish  Vaswani ", "Shazeer, N.", "  "},
				Venue:   " NeurIPS ",
			},
			{Raw: "Second entry", Title: "Ends with ellipsis...", Authors: nil},
		},
	}, WithClock(fixedClock))

	first := got.References[0]
	if want := "Vaswani, A., Shazeer, N. Attention Is All You Need. NeurIPS, 2017."; first.Raw != want {
		t.Errorf("Raw = %q,\nwant %q (内容は変えず空白だけ揃えること)", first.Raw, want)
	}
	if first.Title != "Attention Is All You Need" {
		t.Errorf("Title = %q, want 末尾のピリオドを 1 つ除いた値", first.Title)
	}
	// 姓名の順序や省略は揃えず (誤変換で情報を壊す)、空要素だけ除く
	if want := []string{"Ashish Vaswani", "Shazeer, N."}; !reflect.DeepEqual(first.Authors, want) {
		t.Errorf("Authors = %q, want %q", first.Authors, want)
	}
	if first.Venue != "NeurIPS" {
		t.Errorf("Venue = %q", first.Venue)
	}

	second := got.References[1]
	if second.Title != "Ends with ellipsis..." {
		t.Errorf("Title = %q, want 省略記号は残すこと", second.Title)
	}
	if second.Authors != nil {
		t.Errorf("Authors = %v, want nil (omitempty のフィールドは nil のまま)", second.Authors)
	}
}

func strPtr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
