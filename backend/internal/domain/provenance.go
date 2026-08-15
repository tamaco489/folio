package domain

import "time"

// Route は抽出経路
type Route string

const (
	// RouteTextract は経路 A。Textract の出力を Bedrock で構造化する
	RouteTextract Route = "textract"

	// RouteBedrock は経路 B。ページ画像を Bedrock へ直接投入する
	RouteBedrock Route = "bedrock"
)

// Provenance は抽出の由来
//
// 同一の PDF が両経路を並走するため、どちらの経路の結果かをここで判別する
type Provenance struct {
	Route       Route      `json:"route"`
	ExtractedAt time.Time  `json:"extractedAt"`
	Confidence  Confidence `json:"confidence"`
	Cost        Cost       `json:"cost"`
	DurationMs  int64      `json:"durationMs"`
	Warnings    []string   `json:"warnings,omitempty"`
}

// Confidence はフィールドごとの信頼度 (0.0 - 1.0)
//
// 算出できなかったフィールドと信頼度 0 を区別するためポインタとする
// 該当要素が原本に存在しない場合に 0 を入れると、レビュー要否の判定が誤って低信頼側へ倒れる
type Confidence struct {
	Title      *float64 `json:"title,omitempty"`
	Authors    *float64 `json:"authors,omitempty"`
	Abstract   *float64 `json:"abstract,omitempty"`
	Sections   *float64 `json:"sections,omitempty"`
	Figures    *float64 `json:"figures,omitempty"`
	Tables     *float64 `json:"tables,omitempty"`
	References *float64 `json:"references,omitempty"`
}

// Cost は課金対象の使用量
//
// 単価はモデルとリージョンで変わるため金額に換算せず、使用量のまま保持する
type Cost struct {
	// Textract の使用量は経路 A でのみ発生する
	TextractPages    int      `json:"textractPages,omitempty"`
	TextractFeatures []string `json:"textractFeatures,omitempty"`

	BedrockModel        string `json:"bedrockModel"`
	BedrockInputTokens  int    `json:"bedrockInputTokens"`
	BedrockOutputTokens int    `json:"bedrockOutputTokens"`
}
