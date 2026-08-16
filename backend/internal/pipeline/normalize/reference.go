package normalize

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// minYear は出版年として受け入れる下限
//
// 活版印刷以降の文献を余裕をもって含める
// 上限は現在の翌年とする (採録済みで翌年に出版される文献が引かれる)
const minYear = 1500

// doiPattern は DOI の形式
//
// Crossref が案内する現代の DOI の正規表現 (^10.\d{4,9}/[-._;()/:A-Z0-9]+$) に倣い、接頭辞 10. と 4 - 9 桁の登録者番号、スラッシュ以降の接尾辞を要求する
// 接尾辞の文字種は古い DOI に例外があるため空白以外を許す
var doiPattern = regexp.MustCompile(`^10\.\d{4,9}/\S+$`)

// doiPrefixes は DOI の前に付きうる表記 (小文字化してから照合する)
//
// "doi: https://doi.org/10.x" のように重なる場合があるため、いずれかが取れる限り剥がし続ける
var doiPrefixes = []string{
	"https://doi.org/",
	"http://doi.org/",
	"https://dx.doi.org/",
	"http://dx.doi.org/",
	"doi.org/",
	"doi:",
}

func (n *normalizer) references(refs []domain.Reference) []domain.Reference {
	out := make([]domain.Reference, 0, len(refs))
	for i, r := range refs {
		out = append(out, domain.Reference{
			Raw:     collapseSpace(r.Raw),
			Title:   referenceTitle(r.Title),
			Authors: collapseStrings(r.Authors),
			Year:    n.year(r.Year, fmt.Sprintf("references[%d]", i)),
			Venue:   collapseSpace(r.Venue),
			DOI:     n.doi(r.DOI, i),
		})
	}
	return out
}

// referenceTitle は題名の空白を揃え、末尾のピリオドを 1 つ除く
//
// 片方の経路だけが原本の句点まで題名に含めるケースが比較の雑音になる
// 末尾が ".." で終わる場合は省略記号とみなして触らない (1 つずつ剥がすと掛けるたびに結果が変わり冪等でなくなる)
func referenceTitle(s string) string {
	s = collapseSpace(s)
	if strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "..") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	}
	return s
}

// doi は DOI の表記を揃える
//
// URL や "doi:" の接頭辞を剥がして小文字にする (DOI は大文字小文字を区別しない)
// 剥がした後が DOI の形式でなければ nil に戻して警告する (誤った DOI で外部照合すると偽陽性になる)
// 空文字は未特定と同義であるため黙って nil にする
func (n *normalizer) doi(p *string, i int) *string {
	if p == nil {
		return nil
	}
	s := strings.ToLower(collapseSpace(*p))
	if s == "" {
		return nil
	}

	for stripped := true; stripped; {
		stripped = false
		for _, prefix := range doiPrefixes {
			if strings.HasPrefix(s, prefix) {
				s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
				stripped = true
			}
		}
	}

	if !doiPattern.MatchString(s) {
		n.warnf("references[%d] の doi が DOI の形式でないため未特定に戻した: %q", i, *p)
		return nil
	}
	return &s
}

// year は出版年の妥当性を確かめる
//
// 4 桁の年として妥当な範囲を外れる値は抽出の誤りとみなし、未設定 (0) に戻して警告する
// 0 は未設定の意味であるため触らない
func (n *normalizer) year(y int, where string) int {
	if y == 0 {
		return 0
	}
	if y >= minYear && y <= n.now().Year()+1 {
		return y
	}
	n.warnf("%s の year が範囲外のため未設定に戻した: %d", where, y)
	return 0
}
