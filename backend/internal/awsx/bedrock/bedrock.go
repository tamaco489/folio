// Package bedrock は Bedrock Runtime の操作を awsx 層に閉じ込める
//
// 経路 A (Textract の出力テキストの構造化) と経路 B (ページ画像の直接読み取り) は
// 入力の種類が異なるだけで呼び出し方は同じであるため、Converse API を用いて
// テキストと画像を同一の Request にまとめて扱う
package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

var (
	// ErrModelIDRequired はモデル ID が Request にも既定値にも指定されていない場合に返る
	ErrModelIDRequired = errors.New("bedrock: model id is required")
	// ErrEmptyRequest はメッセージが 1 件もない場合に返る
	ErrEmptyRequest = errors.New("bedrock: request has no messages")
	// ErrEmptyContent は中身のない content block が含まれる場合に返る
	ErrEmptyContent = errors.New("bedrock: content block is empty")
	// ErrUnsupportedContent は未知の content block 種別が渡された場合に返る
	ErrUnsupportedContent = errors.New("bedrock: unsupported content block")
	// ErrNoTextContent はモデルの応答にテキストが含まれない場合に返る
	ErrNoTextContent = errors.New("bedrock: response has no text content")
	// ErrInvalidJSON はモデルの応答を JSON として解釈できない場合に返る
	ErrInvalidJSON = errors.New("bedrock: response is not valid json")
)

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
// テキストと画像を同じ配列に並べられるため、経路 A と経路 B で
// 呼び出し側のコードが変わらない
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

// Request は Converse API への入力
type Request struct {
	// ModelID は使用するモデル。空の場合はクライアントの既定値を用いる
	ModelID string
	// System はシステムプロンプト
	System string
	// Messages は会話履歴
	Messages []Message
	// MaxTokens はモデルの出力トークン上限
	MaxTokens *int32
	// Temperature は出力のばらつき
	Temperature *float32
	// RecordKey は記録・再生時のファイル名部分。RecordKey 関数で組み立てる
	RecordKey string
}

// Usage は課金の記録に用いるトークン数
type Usage struct {
	InputTokens  int32 `json:"inputTokens"`
	OutputTokens int32 `json:"outputTokens"`
	TotalTokens  int32 `json:"totalTokens"`
}

// Response は Converse API の応答を呼び出し側に必要な形へ落としたもの
type Response struct {
	// Text は応答に含まれるテキスト content block の連結
	Text string `json:"text"`
	// StopReason は生成が止まった理由
	StopReason string `json:"stopReason"`
	// Usage は provenance.cost に記録するトークン数
	Usage Usage `json:"usage"`
	// LatencyMs は Bedrock が報告した所要時間
	LatencyMs int64 `json:"latencyMs"`
	// Attempts は成功までに要した試行回数 (初回を 1 とする)
	Attempts int `json:"attempts,omitempty"`
}

// ConverseAPI は bedrockruntime.Client のうち本パッケージが利用する範囲
//
// テストでフェイクに差し替えるためにインタフェースとして切り出す
type ConverseAPI interface {
	Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// Converser は経路 A と経路 B の双方が依存する呼び出し口
//
// 実クライアント、記録モード、再生モードのいずれもこれを満たす
type Converser interface {
	Converse(ctx context.Context, req Request) (*Response, error)
}

// Client は awsx 層が公開する Bedrock Runtime クライアント
type Client struct {
	api            ConverseAPI
	defaultModelID string
	retry          RetryConfig
	sleep          Sleeper
	randN          func(int64) int64
}

var _ Converser = (*Client)(nil)

// Option は Client の設定を差し替える
type Option func(*Client)

// WithDefaultModelID は Request でモデル ID が省略された場合の既定値を設定する
//
// モデル ID は internal/config から渡す前提であり、本パッケージには持たない
func WithDefaultModelID(id string) Option {
	return func(c *Client) { c.defaultModelID = id }
}

// WithRetry はリトライ設定を差し替える
func WithRetry(rc RetryConfig) Option {
	return func(c *Client) { c.retry = rc }
}

// WithSleeper は待機処理を差し替える。テストで実時間を待たないために用いる
func WithSleeper(s Sleeper) Option {
	return func(c *Client) {
		if s != nil {
			c.sleep = s
		}
	}
}

// WithRandN はジッタの乱数源を差し替える。テストで待機時間を決定的にするために用いる
func WithRandN(f func(int64) int64) Option {
	return func(c *Client) {
		if f != nil {
			c.randN = f
		}
	}
}

// New は SDK のクライアントをラップする
func New(api ConverseAPI, opts ...Option) *Client {
	c := &Client{
		api:   api,
		retry: DefaultRetryConfig(),
		sleep: RealSleeper,
		randN: defaultRandN,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Converse はテキストと画像のいずれの入力も同じ経路で Bedrock に送る
//
// スロットリングなどの一時的な失敗は指数バックオフでリトライする
func (c *Client) Converse(ctx context.Context, req Request) (*Response, error) {
	in, err := c.buildInput(req)
	if err != nil {
		return nil, err
	}

	attempts := max(c.retry.MaxAttempts, 1)

	var lastErr error
	for attempt := 1; ; attempt++ {
		out, err := c.api.Converse(ctx, in)
		if err == nil {
			resp, perr := newResponse(out)
			if perr != nil {
				return nil, perr
			}
			resp.Attempts = attempt
			return resp, nil
		}

		lastErr = err
		if !IsRetryable(err) {
			return nil, fmt.Errorf("bedrock: converse: %w", err)
		}
		if attempt >= attempts {
			return nil, fmt.Errorf("bedrock: converse: %w (%d attempts): %w", ErrRetryExhausted, attempts, lastErr)
		}
		if werr := c.sleep(ctx, c.retry.delay(attempt, c.randN)); werr != nil {
			return nil, fmt.Errorf("bedrock: converse: backoff interrupted: %w (last error: %w)", werr, lastErr)
		}
	}
}

func (c *Client) buildInput(req Request) (*bedrockruntime.ConverseInput, error) {
	modelID := req.ModelID
	if modelID == "" {
		modelID = c.defaultModelID
	}
	if modelID == "" {
		return nil, ErrModelIDRequired
	}
	if len(req.Messages) == 0 {
		return nil, ErrEmptyRequest
	}

	messages := make([]types.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		blocks, err := toContentBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		role := m.Role
		if role == "" {
			role = RoleUser
		}
		messages = append(messages, types.Message{
			Role:    types.ConversationRole(role),
			Content: blocks,
		})
	}

	in := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(modelID),
		Messages: messages,
	}
	if req.System != "" {
		in.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: req.System},
		}
	}
	if req.MaxTokens != nil || req.Temperature != nil {
		in.InferenceConfig = &types.InferenceConfiguration{
			MaxTokens:   req.MaxTokens,
			Temperature: req.Temperature,
		}
	}
	return in, nil
}

func toContentBlocks(parts []ContentPart) ([]types.ContentBlock, error) {
	if len(parts) == 0 {
		return nil, ErrEmptyContent
	}
	blocks := make([]types.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case TextPart:
			if v.Text == "" {
				return nil, fmt.Errorf("%w: text", ErrEmptyContent)
			}
			blocks = append(blocks, &types.ContentBlockMemberText{Value: v.Text})
		case ImagePart:
			if len(v.Bytes) == 0 {
				return nil, fmt.Errorf("%w: image", ErrEmptyContent)
			}
			if v.Format == "" {
				return nil, fmt.Errorf("%w: image format", ErrEmptyContent)
			}
			blocks = append(blocks, &types.ContentBlockMemberImage{
				Value: types.ImageBlock{
					Format: types.ImageFormat(v.Format),
					Source: &types.ImageSourceMemberBytes{Value: v.Bytes},
				},
			})
		default:
			return nil, fmt.Errorf("%w: %T", ErrUnsupportedContent, p)
		}
	}
	return blocks, nil
}

func newResponse(out *bedrockruntime.ConverseOutput) (*Response, error) {
	if out == nil {
		return nil, ErrNoTextContent
	}

	var sb strings.Builder
	if msg, ok := out.Output.(*types.ConverseOutputMemberMessage); ok {
		for _, block := range msg.Value.Content {
			if t, ok := block.(*types.ContentBlockMemberText); ok {
				sb.WriteString(t.Value)
			}
		}
	}
	if sb.Len() == 0 {
		return nil, ErrNoTextContent
	}

	resp := &Response{
		Text:       sb.String(),
		StopReason: string(out.StopReason),
	}
	if out.Usage != nil {
		resp.Usage = Usage{
			InputTokens:  aws.ToInt32(out.Usage.InputTokens),
			OutputTokens: aws.ToInt32(out.Usage.OutputTokens),
			TotalTokens:  aws.ToInt32(out.Usage.TotalTokens),
		}
	}
	if out.Metrics != nil {
		resp.LatencyMs = aws.ToInt64(out.Metrics.LatencyMs)
	}
	return resp, nil
}

// DecodeJSON は応答テキストを構造化 JSON として v に読み込む
//
// モデルは前置きやコードフェンスを伴う応答を返すことがあるため、
// JSON 部分を切り出してから解釈する。切り出しても解釈できない場合は
// ErrInvalidJSON を返し、生テキストは Response.Text に残したままとする
func (r *Response) DecodeJSON(v any) error {
	payload, ok := extractJSON(r.Text)
	if !ok {
		return fmt.Errorf("%w: no json object found", ErrInvalidJSON)
	}
	if err := json.Unmarshal([]byte(payload), v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	return nil
}

// extractJSON はコードフェンスや前後の散文を取り除いて JSON 部分を返す
func extractJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return s, true
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return "", false
	}
	closing := byte('}')
	if s[start] == '[' {
		closing = ']'
	}
	end := strings.LastIndexByte(s, closing)
	if end < start {
		return "", false
	}
	return s[start : end+1], true
}
