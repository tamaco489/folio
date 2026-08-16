package verify

import (
	"time"

	"github.com/tamaco489/folio/backend/internal/domain"
)

// Report は信頼度の根拠をフィールド単位で残したもの
//
// finalizer が outputs/{jobId}/comparison.json に書く材料であり、経路差の分析とレビュー UI の表示に使う
// Step Functions の Choice に渡すのは NeedsReview だけでよい
type Report struct {
	JobID           string           `json:"jobId"`
	Route           domain.Route     `json:"route"`
	VerifiedAt      time.Time        `json:"verifiedAt"`
	OriginalChecked bool             `json:"originalChecked"` // OriginalChecked は原本との突合を行ったか (テキストレイヤーが無ければ false)
	Fields          Fields           `json:"fields"`
	Hallucinations  []Hallucination  `json:"hallucinations"`
	References      []ReferenceCheck `json:"references"`
	Crossref        CrossrefSummary  `json:"crossref"`
	NeedsReview     bool             `json:"needsReview"`
	ReviewThreshold float64          `json:"reviewThreshold"`
}

// Fields は domain.Confidence と同じ 7 フィールドの根拠
type Fields struct {
	Title      FieldReport `json:"title"`
	Authors    FieldReport `json:"authors"`
	Abstract   FieldReport `json:"abstract"`
	Sections   FieldReport `json:"sections"`
	Figures    FieldReport `json:"figures"`
	Tables     FieldReport `json:"tables"`
	References FieldReport `json:"references"`
}

// FieldReport は 1 フィールドの信頼度と、それを構成したシグナル
//
// Confidence は存在するシグナルの平均であり、無いシグナルは 0 ではなく除外する (経路 B に Textract の確信度が無いことで不当に低くならないようにするため)
// シグナルが 1 つも無ければ Confidence は null のまま
type FieldReport struct {
	Confidence *float64 `json:"confidence"`
	Original   *float64 `json:"original,omitempty"` // Original は原本との一致度 (突合した項目の平均)
	Textract   *float64 `json:"textract,omitempty"` // Textract は経路 A の入力に載っていた Textract の確信度
	Crossref   *float64 `json:"crossref,omitempty"` // Crossref は照合できた参照文献のうち登録と一致した割合 (references のみ)
	Items      int      `json:"items"`              // Items は原本と突合した項目数 (0 なら該当要素が無いか突合を省略した)
}

// Hallucination は原本に見つからなかった抽出値
type Hallucination struct {
	Path  string  `json:"path"`  // Path は domain.Document 内の位置 (例: metadata.authors[1].name)
	Value string  `json:"value"` // Value は抽出値
	Score float64 `json:"score"` // Score は原本との一致度 (Crossref の不一致では登録題名との一致度)
}

// ReferenceStatus は参照文献 1 件の外部照合の結果
type ReferenceStatus string

const (
	// StatusVerified は Crossref の登録と一致した
	StatusVerified ReferenceStatus = "verified"

	// StatusMismatch は DOI は実在するが登録されている題名が抽出した記載と別物であった (DOI が別の文献を指している)
	StatusMismatch ReferenceStatus = "mismatch"

	// StatusNotFound は Crossref に登録が無い (DOI が 404、または題名検索の候補に一致が無い)
	//
	// arXiv の DOI は DataCite 登録のためここに入る。未確認であってハルシネーションではない
	StatusNotFound ReferenceStatus = "notFound"

	// StatusUnavailable は Crossref が応答せず照合できなかった
	StatusUnavailable ReferenceStatus = "unavailable"

	// StatusSkipped は照合を試みなかった (件数の上限を超えた、照合の材料が無い、Resolver が無い)
	StatusSkipped ReferenceStatus = "skipped"
)

// ReferenceCheck は参照文献 1 件の外部照合の根拠
type ReferenceCheck struct {
	Index        int             `json:"index"`
	DOI          string          `json:"doi,omitempty"`   // DOI は抽出された DOI
	Query        string          `json:"query,omitempty"` // Query は DOI が無い場合に検索へ渡した文字列
	Status       ReferenceStatus `json:"status"`
	MatchedDOI   string          `json:"matchedDoi,omitempty"`   // MatchedDOI は Crossref が返した DOI
	MatchedTitle string          `json:"matchedTitle,omitempty"` // MatchedTitle は Crossref に登録されている題名
	Similarity   *float64        `json:"similarity,omitempty"`   // Similarity は登録題名と抽出した記載の一致度
	Detail       string          `json:"detail,omitempty"`       // Detail は unavailable の原因や skipped の理由
}

// CrossrefSummary は外部照合の件数
type CrossrefSummary struct {
	Verified    int `json:"verified"`
	Mismatch    int `json:"mismatch"`
	NotFound    int `json:"notFound"`
	Unavailable int `json:"unavailable"`
	Skipped     int `json:"skipped"`
}
