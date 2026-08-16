package normalize

import (
	"regexp"
	"strings"
)

// maxHeadingLevel は見出しレベルの上限
//
// LaTeX の番号付き見出しは section / subsection / subsubsection の 3 段であり、paragraph まで数えても 4 段に収まる
const maxHeadingLevel = 4

// headingNumber は見出しの先頭に置かれた章番号
//
// 番号の直後は空白か文末であること (末尾のピリオドの有無は問わない)
// 想定する形式は次のとおり
//   - アラビア数字: 1 / 1. / 3.1 / 3.1.2 (ACL, NeurIPS, ICLR スタイルの本文)
//   - 英大文字 1 字を先頭に置く付録: A / A. / A.2 (同スタイルの付録)
//   - ローマ数字: I / II. / II.3 (IEEE スタイルの本文)
//
// 数字を 2 桁までに限るのは "2019 Shared Task Overview" のような年や数量で始まる見出しを番号と誤認しないため
// 英大文字 1 字は "A Simple Baseline" のような冠詞で始まる見出しも番号とみなす (付録の "A Proofs" と字面で区別できず、番号の要素数から機械的に決める指針を優先する)
// IEEE スタイルの節見出し "A. Motivation" も要素数 1 でレベル 1 になる (付録との区別には見出し文字列以外の文脈が要るため、ここでは賢くしない)
// 全角数字や "1)" "§1" の形式は番号とみなさず、経路のレベルを保つ
var headingNumber = regexp.MustCompile(`^(?:\d{1,2}|[A-Z]|[IVX]+)(?:\.\d{1,2})*\.?(?:\s|$)`)

// headingLevel は見出しのレベルを決める
//
// 番号付きの見出しは番号の要素数から機械的に決める (3.1 は 2)
// 番号は経路が LAYOUT の論理ブロックとモデルの判断のどちらから読んだかによらず客観的に決まるため、ここで揃えないと同じ章に別のレベルが付いて比較を汚す
// 番号のない見出しは経路が付けたレベルをそのまま返す (経路の解釈の差は比較対象として残す)
func headingLevel(heading string, level int) int {
	number := headingNumber.FindString(heading)
	if number == "" {
		return level
	}
	number = strings.TrimRight(strings.TrimSpace(number), ".")
	return strings.Count(number, ".") + 1
}
