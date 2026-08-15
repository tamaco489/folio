package bedrockroute

import (
	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/domain"
)

// PageResult は 1 ページ分の抽出結果
//
// Map の 1 要素の出力であり Merge の入力でもあるため、S3 を経由して JSON で往復する
// State 間は 256KB 上限に掛かるため、実体は S3 に置き State には S3 キーだけを流す
type PageResult struct {
	Page int `json:"page"` // Page は 1 始まりのページ番号 (モデルの申告ではなく呼び出し側の指定で埋める)

	// 書誌情報は表紙にしか現れないため、他のページではゼロ値になる
	Title    string          `json:"title,omitempty"`
	Authors  []domain.Author `json:"authors,omitempty"`
	Abstract string          `json:"abstract,omitempty"`
	Keywords []string        `json:"keywords,omitempty"`

	Sections   []PageSection   `json:"sections,omitempty"`
	Figures    []PageFigure    `json:"figures,omitempty"`
	Tables     []PageTable     `json:"tables,omitempty"`
	References []PageReference `json:"references,omitempty"`

	// ContinuesPreviousSection はページ冒頭の見出しなし本文が前ページの章の続きかを表す
	//
	// 欠落 (nil) は続きとみなす — 本文がページを跨ぐのは通常であり、明示されない限り分断しない方が誤りが少ない
	// 欠落と false を区別する必要があるためポインタとする
	ContinuesPreviousSection *bool `json:"continuesPreviousSection,omitempty"`

	// ContinuesPreviousReference はページ冒頭の参照文献が前ページ末尾の 1 件の続きかを表す
	//
	// 欠落 (nil) は続きでないとみなし、既定を ContinuesPreviousSection と逆にする
	// 参照文献はページ先頭で新しい 1 件が始まる方が通常であり、既定で連結すると別々の文献が 1 件に潰れる
	ContinuesPreviousReference *bool `json:"continuesPreviousReference,omitempty"`

	ModelID string        `json:"modelId,omitempty"` // ModelID は provenance.cost.bedrockModel へ引き継ぐ
	Usage   bedrock.Usage `json:"usage"`             // Usage は Merge で合算し provenance.cost へ記録する
}

// PageSection はページ上の見出しと本文の 1 ブロック
//
// 見出しのないブロックは直前の章の続きを表す
type PageSection struct {
	Level   int    `json:"level,omitempty"`
	Heading string `json:"heading,omitempty"`
	Text    string `json:"text,omitempty"`
}

// PageFigure はページ上の図
//
// 座標は経路 B では取得できないため持たず、domain.Figure.BBox は空のままとする
type PageFigure struct {
	Label   string `json:"label"` // Label は原本に印字された図番号 (例: Figure 1, 図 1) で domain.Figure.ID になる
	Caption string `json:"caption"`
}

// PageTable はページ上の表
type PageTable struct {
	Label   string     `json:"label"` // Label は domain.Table.ID になる表番号
	Caption string     `json:"caption"`
	Header  [][]string `json:"header,omitempty"` // Header は多段ヘッダーを表すためヘッダー行の配列とする
	Rows    [][]string `json:"rows,omitempty"`
}

// PageReference はページ上の参照文献 1 件
//
// ページを跨いで分断された 1 件の断片でもありうる
type PageReference struct {
	Raw     string   `json:"raw"`
	Title   string   `json:"title,omitempty"`
	Authors []string `json:"authors,omitempty"`
	Year    int      `json:"year,omitempty"`
	Venue   string   `json:"venue,omitempty"`
	DOI     string   `json:"doi,omitempty"` // DOI は domain.Reference では未特定を null で表すため、空文字を nil へ写す
}
