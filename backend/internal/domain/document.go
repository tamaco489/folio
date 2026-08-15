// Package domain は経路 A と経路 B の抽出結果が共通して到達する構造化 JSON のスキーマを定義する
//
// このパッケージは normalize の出力型であり、両経路の差分を取るための共通表現でもある
// ページ番号は 1 始まりとする
package domain

import "time"

// SchemaVersion は構造化 JSON のスキーマ版
//
// Phase 2 以降のフィールド追加時に、過去に生成した JSON との差を判別するために保持する
const SchemaVersion = "1.0"

// Language は ISO 639-1 の言語コード
type Language string

const (
	// LanguageEnglish は英語
	LanguageEnglish Language = "en"

	// LanguageJapanese は日本語
	//
	// Textract が日本語に非対応のため、この言語の文書は経路 B のみを通る
	LanguageJapanese Language = "ja"
)

// Document は 1 本の PDF に対する抽出結果全体
//
// Sections Figures Tables References は経路間で同じ形の差分を取るため、要素が無い場合も空配列として出力する
type Document struct {
	JobID         string      `json:"jobId"`
	SchemaVersion string      `json:"schemaVersion"`
	Source        Source      `json:"source"`
	Metadata      Metadata    `json:"metadata"`
	Sections      []Section   `json:"sections"`
	Figures       []Figure    `json:"figures"`
	Tables        []Table     `json:"tables"`
	References    []Reference `json:"references"`
	Provenance    Provenance  `json:"provenance"`
}

// Source は入力 PDF と、受領時に決定論的に判明する属性
//
// いずれも抽出ではなくバリデーションと前処理で確定するため、経路によらず必ず埋まる
type Source struct {
	Bucket       string    `json:"bucket"`
	Key          string    `json:"key"`
	Filename     string    `json:"filename"`
	SHA256       string    `json:"sha256"`
	Language     Language  `json:"language"`
	PageCount    int       `json:"pageCount"`
	HasTextLayer bool      `json:"hasTextLayer"`
	UploadedAt   time.Time `json:"uploadedAt"`
}

// Ptr は値をポインタに変換する
//
// 欠落と型のゼロ値を区別するフィールドへ値を設定するために用いる
func Ptr[T any](v T) *T { return &v }
