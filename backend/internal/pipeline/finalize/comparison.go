package finalize

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify"
)

// RouteStatus は経路の結果の状態
type RouteStatus string

const (
	// RouteSucceeded は結果を正規化・検証して outputs/ へ書いた (経路 B はページが欠落していても 1 件でもあれば succeeded とし、不完全さは NeedsReview と MissingPages で表す)
	RouteSucceeded RouteStatus = "succeeded"

	// RouteFailed は結果が得られなかった (Catch の理由か、finalizer 側の理由を残す)
	RouteFailed RouteStatus = "failed"

	// RouteSkipped は経路を通していない (日本語の文書は経路 A を通らない)
	RouteSkipped RouteStatus = "skipped"
)

// ErrorResultNotFound は Catch の理由が無いまま経路の結果が S3 に無かったときに RouteResult.Error に入れる理由
const ErrorResultNotFound = "ResultNotFound"

// Comparison は outputs/{jobId}/comparison.json の内容
//
// Phase 1 は目視比較が目的であり、数値のメトリクスは算出せず差分の可視化に必要な情報だけを載せる (数値評価は Phase 2 の tools の範囲)
type Comparison struct {
	JobID       string        `json:"jobId"`
	FinalizedAt time.Time     `json:"finalizedAt"`
	Status      dynamo.Status `json:"status"` // Status は COMPLETED / REVIEW_PENDING / FAILED
	NeedsReview bool          `json:"needsReview"`
	Routes      Routes        `json:"routes"` // Routes は経路ごとの結果の所在と状態
	Diff        *Diff         `json:"diff"`   // Diff は両経路が揃ったときだけ (片方だけなら null)
}

// Routes は経路ごとの結果
type Routes struct {
	Textract RouteResult `json:"textract"`
	Bedrock  RouteResult `json:"bedrock"`
}

// RouteResult は 1 経路の結果の所在と状態
type RouteResult struct {
	Status       RouteStatus    `json:"status"`
	ResultKey    string         `json:"resultKey,omitempty"` // ResultKey は succeeded のとき outputs/ のキー
	Error        string         `json:"error,omitempty"`     // Error は failed のとき Catch の Error か finalizer 側の理由、経路 B が succeeded でも Map の Catch が理由を残していればその Error
	Cause        string         `json:"cause,omitempty"`
	MissingPages int            `json:"missingPages,omitempty"` // MissingPages は経路 B が succeeded のとき pageCount のうち結果が無かったページ数
	NeedsReview  bool           `json:"needsReview"`
	DurationMs   int64          `json:"durationMs,omitempty"`
	Cost         *domain.Cost   `json:"cost,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	Report       *verify.Report `json:"report,omitempty"` // Report は succeeded のとき verify の根拠
}

// Diff は両経路の値を並べて置く (どちらが正しいかは判定しない)
//
// 参照文献の突合結果は Report.References に既にあるため載せない
type Diff struct {
	Title    ValuePair `json:"title"`
	Authors  ListPair  `json:"authors"` // Authors は著者名の一覧
	Abstract ValuePair `json:"abstract"`
	Counts   Counts    `json:"counts"`
	Headings ListPair  `json:"headings"` // Headings は章見出しの一覧 (構造の違いを一目で見るため)
}

// ValuePair は 1 つの値の両経路の並び
type ValuePair struct {
	Textract string `json:"textract"`
	Bedrock  string `json:"bedrock"`
	Equal    bool   `json:"equal"` // Equal は空白を畳んで大文字小文字を無視した比較
}

// ListPair は文字列の一覧の両経路の並び
type ListPair struct {
	Textract []string `json:"textract"`
	Bedrock  []string `json:"bedrock"`
	Equal    bool     `json:"equal"` // Equal は要素数と順序を含め、各要素を ValuePair と同じ規則で比較した結果
}

// CountPair は件数の両経路の並び
type CountPair struct {
	Textract int  `json:"textract"`
	Bedrock  int  `json:"bedrock"`
	Equal    bool `json:"equal"`
}

// Counts は要素の件数
type Counts struct {
	Sections   CountPair `json:"sections"`
	Figures    CountPair `json:"figures"`
	Tables     CountPair `json:"tables"`
	References CountPair `json:"references"`
}

// diff は正規化・検証後の両経路の文書から Diff を組み立てる
func diff(textract, bedrock domain.Document) Diff {
	return Diff{
		Title:    valuePair(textract.Metadata.Title, bedrock.Metadata.Title),
		Authors:  listPair(authorNames(textract.Metadata.Authors), authorNames(bedrock.Metadata.Authors)),
		Abstract: valuePair(textract.Metadata.Abstract, bedrock.Metadata.Abstract),
		Counts: Counts{
			Sections:   countPair(len(textract.Sections), len(bedrock.Sections)),
			Figures:    countPair(len(textract.Figures), len(bedrock.Figures)),
			Tables:     countPair(len(textract.Tables), len(bedrock.Tables)),
			References: countPair(len(textract.References), len(bedrock.References)),
		},
		Headings: listPair(headings(textract.Sections), headings(bedrock.Sections)),
	}
}

func valuePair(a, b string) ValuePair {
	return ValuePair{Textract: a, Bedrock: b, Equal: equalText(a, b)}
}

func listPair(a, b []string) ListPair {
	return ListPair{Textract: a, Bedrock: b, Equal: equalList(a, b)}
}

func countPair(a, b int) CountPair {
	return CountPair{Textract: a, Bedrock: b, Equal: a == b}
}

// authorNames は著者名だけを取り出す (所属とメールは経路で埋まり方が違いすぎるため並べない)
func authorNames(authors []domain.Author) []string {
	names := make([]string, 0, len(authors))
	for _, a := range authors {
		names = append(names, a.Name)
	}
	return names
}

func headings(sections []domain.Section) []string {
	out := make([]string, 0, len(sections))
	for _, s := range sections {
		out = append(out, s.Heading)
	}
	return out
}

// equalText は空白の連続を 1 つに畳み、大文字小文字を無視して比べる
//
// 大文字化や折り返しの違いは経路の解釈の差ではなく表記のゆれとして扱う (normalize は空白を揃えるが大文字小文字は揃えない)
func equalText(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// equalList は要素数が同じで、対応する要素がすべて equalText で一致するかを返す (順序も比較の対象)
func equalList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalText(a[i], b[i]) {
			return false
		}
	}
	return true
}

// catch は Step Functions の Catch が ResultPath に置く失敗の理由
type catch struct {
	Error string
	Cause string
}

// parseCatch は経路のブランチの出力から Catch の失敗理由を取り出す
//
// Error が非空の文字列であるオブジェクトだけを失敗の理由とみなす (Catch は Error / Cause を出すため、キーの大文字小文字は問わずこの 2 つを見る)
// 成功時の出力 (経路 A の resultKey を持つオブジェクト、Map の配列) や null は理由なしとして無視する
func parseCatch(raw json.RawMessage) (catch, bool) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return catch{}, false
	}
	var c catch
	for key, value := range fields {
		var s string
		if json.Unmarshal(value, &s) != nil {
			continue
		}
		switch {
		case strings.EqualFold(key, "error"):
			c.Error = s
		case strings.EqualFold(key, "cause"):
			c.Cause = s
		}
	}
	if c.Error == "" {
		return catch{}, false
	}
	return c, true
}
