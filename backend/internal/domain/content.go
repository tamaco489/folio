package domain

// Metadata は書誌情報
//
// Title Authors Abstract は経路間の比較の中核であり、抽出できなかった場合も欠落させずゼロ値で出力する
// キーの有無が経路で変わると差分が取れないため
type Metadata struct {
	Title           string   `json:"title"`
	Authors         []Author `json:"authors"`
	Abstract        string   `json:"abstract"`
	Keywords        []string `json:"keywords,omitempty"`
	Venue           string   `json:"venue,omitempty"`
	Year            int      `json:"year,omitempty"`
	PrimaryCategory string   `json:"primaryCategory,omitempty"`
}

// Author は著者 1 名
type Author struct {
	Name        string `json:"name"`
	Affiliation string `json:"affiliation,omitempty"`
	Email       string `json:"email,omitempty"`
}

// Section は章見出しと本文
//
// Pages は本文が跨るページ番号を昇順で持つ
type Section struct {
	Level   int    `json:"level"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
	Pages   []int  `json:"pages"`
}

// BBox はページ上の矩形領域を [x0, y0, x1, y1] の 4 要素で表す
//
// 原点はページ左上、値はページの幅と高さをそれぞれ 1 とした正規化座標とする
// ページサイズや解像度に依存せず原本上の位置を再現でき、レビュー UI でのハイライトにそのまま使える
//
// 経路 B は座標を保持できないため、欠落しうる
type BBox []float64

// Figure は図
type Figure struct {
	ID      string `json:"id"`
	Caption string `json:"caption"`
	Page    int    `json:"page"`
	BBox    BBox   `json:"bbox,omitempty"`
}

// Table は表
type Table struct {
	ID      string `json:"id"`
	Caption string `json:"caption"`
	Page    int    `json:"page"`
	BBox    BBox   `json:"bbox,omitempty"`

	// Header は多段ヘッダーを表現するため、ヘッダー行の配列とする
	//
	// セル結合は復元後の値を各セルへ複製して表す
	Header [][]string `json:"header"`
	Rows   [][]string `json:"rows"`
}

// Reference は参照文献 1 件
//
// Raw は原本の記載をそのまま保持し、解析後の各フィールドの検証に用いる
type Reference struct {
	Raw     string   `json:"raw"`
	Title   string   `json:"title,omitempty"`
	Authors []string `json:"authors,omitempty"`
	Year    int      `json:"year,omitempty"`
	Venue   string   `json:"venue,omitempty"`

	// DOI は外部照合を試みて特定できなかったことを null として残すため、他の任意フィールドと異なり欠落時もキーを出力する
	DOI *string `json:"doi"`
}
