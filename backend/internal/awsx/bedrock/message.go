package bedrock

import "encoding/json"

// Role は Converse API の会話ロール
type Role string

const RoleUser Role = "user"

// ImageFormat は画像 content block の形式
//
// ラスタライズは pdftoppm の -png 固定であるため PNG のみを定義する
type ImageFormat string

const ImageFormatPNG ImageFormat = "png"

// ContentPart はメッセージを構成する content block
//
// テキストと画像を同じ配列に並べられるため、経路 A と経路 B で呼び出し側のコードが変わらない
type ContentPart interface {
	isContentPart()
}

// TextPart はテキストの content block
type TextPart struct {
	Text string
}

func (TextPart) isContentPart() {}

// ImagePart は画像の content block
type ImagePart struct {
	Format ImageFormat
	Bytes  []byte
}

func (ImagePart) isContentPart() {}

// Message は 1 ターン分の入力
type Message struct {
	Role    Role
	Content []ContentPart
}

// UserText はテキストのみの user メッセージを組み立てる (経路 A 向け)
func UserText(s string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{TextPart{Text: s}}}
}

// UserImage は画像と指示テキストを組にした user メッセージを組み立てる (経路 B 向け)
func UserImage(format ImageFormat, b []byte, prompt string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{
		ImagePart{Format: format, Bytes: b},
		TextPart{Text: prompt},
	}}
}

// ToolSpec は応答を tool use の入力として受け取るための出力スキーマ
//
// 自由文に JSON を書かせると本文中の引用符をエスケープしないまま文字列へ転記し壊れた JSON が返ることがあるため、JSON の整形を Bedrock 側に保証させる
type ToolSpec struct {
	Name        string         // Name はモデルに呼ばせる tool の名前
	Description string         // Description は tool の用途 (空なら送らない)
	Schema      map[string]any // Schema は tool の入力を表す JSON Schema (最上位は object で、strict のため全 object に additionalProperties: false を持つ)
}

// Request は Converse API への入力
type Request struct {
	ModelID     string    // ModelID は使用するモデル (空ならクライアントの既定値を用いる)
	System      string    // System はシステムプロンプト
	Messages    []Message // Messages は会話履歴
	MaxTokens   *int32    // MaxTokens はモデルの出力トークン上限
	Temperature *float32  // Temperature は出力のばらつき
	Tool        *ToolSpec // Tool は応答を受け取る tool の定義 (nil なら自由文で受け取る)
	RecordKey   string    // RecordKey は記録・再生時のファイル名部分 (RecordKey 関数で組み立てる)
}

// Usage は課金の記録に用いるトークン数
type Usage struct {
	InputTokens  int32 `json:"inputTokens"`
	OutputTokens int32 `json:"outputTokens"`
	TotalTokens  int32 `json:"totalTokens"`
}

// StopReasonMaxTokens は生成が Request.MaxTokens に達して打ち切られたことを示す StopReason の値
const StopReasonMaxTokens = "max_tokens"

// Response は Converse API の応答を呼び出し側に必要な形へ落としたもの
type Response struct {
	Text       string          `json:"text,omitempty"`      // Text は応答に含まれるテキスト content block の連結
	ToolInput  json.RawMessage `json:"toolInput,omitempty"` // ToolInput は toolUse content block の input (Request.Tool を指定した場合に入る)
	StopReason string          `json:"stopReason"`          // StopReason は生成が止まった理由
	Usage      Usage           `json:"usage"`               // Usage は provenance.cost に記録するトークン数
	LatencyMs  int64           `json:"latencyMs"`           // LatencyMs は Bedrock が報告した所要時間
	Attempts   int             `json:"attempts,omitempty"`  // Attempts は成功までに要した試行回数 (初回を 1 とする)
}
