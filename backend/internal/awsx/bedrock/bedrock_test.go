package bedrock

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awsbedrockruntimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// fakeAPI は ConverseAPI のフェイク (実 API は一切呼ばない)
type fakeAPI struct {
	inputs  []*awsbedrockruntime.ConverseInput
	outputs []*awsbedrockruntime.ConverseOutput
	errs    []error
}

func (f *fakeAPI) Converse(_ context.Context, params *awsbedrockruntime.ConverseInput, _ ...func(*awsbedrockruntime.Options)) (*awsbedrockruntime.ConverseOutput, error) {
	i := len(f.inputs)
	f.inputs = append(f.inputs, params)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.outputs) && f.outputs[i] != nil {
		return f.outputs[i], nil
	}
	return textOutput("{}"), nil
}

func (f *fakeAPI) calls() int { return len(f.inputs) }

func textOutput(text string) *awsbedrockruntime.ConverseOutput {
	return &awsbedrockruntime.ConverseOutput{
		Output: &awsbedrockruntimetypes.ConverseOutputMemberMessage{
			Value: awsbedrockruntimetypes.Message{
				Role:    awsbedrockruntimetypes.ConversationRoleAssistant,
				Content: []awsbedrockruntimetypes.ContentBlock{&awsbedrockruntimetypes.ContentBlockMemberText{Value: text}},
			},
		},
		StopReason: awsbedrockruntimetypes.StopReasonEndTurn,
		Usage: &awsbedrockruntimetypes.TokenUsage{
			InputTokens:  aws.Int32(1200),
			OutputTokens: aws.Int32(340),
			TotalTokens:  aws.Int32(1540),
		},
		Metrics: &awsbedrockruntimetypes.ConverseMetrics{LatencyMs: aws.Int64(4321)},
	}
}

func TestConverseTextInput(t *testing.T) {
	api := &fakeAPI{outputs: []*awsbedrockruntime.ConverseOutput{textOutput(`{"title":"ok"}`)}}
	c := New(api, WithDefaultModelID("model-from-config"))

	resp, err := c.Converse(context.Background(), Request{
		System:      "structure the document",
		Messages:    []Message{UserText("PAGE 1 ...")},
		MaxTokens:   aws.Int32(4096),
		Temperature: aws.Float32(0),
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}

	if got := aws.ToString(api.inputs[0].ModelId); got != "model-from-config" {
		t.Errorf("ModelId = %q, want model-from-config", got)
	}
	if len(api.inputs[0].System) != 1 {
		t.Fatalf("System blocks = %d, want 1", len(api.inputs[0].System))
	}
	if api.inputs[0].InferenceConfig == nil || aws.ToInt32(api.inputs[0].InferenceConfig.MaxTokens) != 4096 {
		t.Errorf("InferenceConfig not propagated: %+v", api.inputs[0].InferenceConfig)
	}

	content := api.inputs[0].Messages[0].Content
	if len(content) != 1 {
		t.Fatalf("content blocks = %d, want 1", len(content))
	}
	text, ok := content[0].(*awsbedrockruntimetypes.ContentBlockMemberText)
	if !ok {
		t.Fatalf("content[0] = %T, want text block", content[0])
	}
	if text.Value != "PAGE 1 ..." {
		t.Errorf("text = %q", text.Value)
	}

	if resp.Usage.InputTokens != 1200 || resp.Usage.OutputTokens != 340 || resp.Usage.TotalTokens != 1540 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.LatencyMs != 4321 {
		t.Errorf("LatencyMs = %d, want 4321", resp.LatencyMs)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	if resp.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", resp.Attempts)
	}
}

// 経路 B の画像入力が経路 A と同じ Request 型で扱えることを確かめる
func TestConverseMultimodalInput(t *testing.T) {
	api := &fakeAPI{}
	c := New(api)

	png := []byte{0x89, 'P', 'N', 'G'}
	if _, err := c.Converse(context.Background(), Request{
		ModelID:  "model-per-request",
		Messages: []Message{UserImage(ImageFormatPNG, png, "read this page")},
	}); err != nil {
		t.Fatalf("Converse: %v", err)
	}

	if got := aws.ToString(api.inputs[0].ModelId); got != "model-per-request" {
		t.Errorf("ModelId = %q, want model-per-request (request overrides default)", got)
	}
	content := api.inputs[0].Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(content))
	}
	img, ok := content[0].(*awsbedrockruntimetypes.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("content[0] = %T, want image block", content[0])
	}
	if img.Value.Format != awsbedrockruntimetypes.ImageFormatPng {
		t.Errorf("format = %q, want png", img.Value.Format)
	}
	src, ok := img.Value.Source.(*awsbedrockruntimetypes.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("source = %T, want bytes", img.Value.Source)
	}
	if string(src.Value) != string(png) {
		t.Errorf("image bytes = %v", src.Value)
	}
	if _, ok := content[1].(*awsbedrockruntimetypes.ContentBlockMemberText); !ok {
		t.Fatalf("content[1] = %T, want text block", content[1])
	}
}

func TestConverseValidation(t *testing.T) {
	tests := map[string]struct {
		req  Request
		want error
	}{
		"異常系_モデル ID が Request にも既定値にもない場合_ErrModelIDRequired が返ること": {
			req:  Request{Messages: []Message{UserText("x")}},
			want: ErrModelIDRequired,
		},
		"異常系_メッセージが 1 件もない場合_ErrEmptyRequest が返ること": {
			req:  Request{ModelID: "m"},
			want: ErrEmptyRequest,
		},
		"異常系_テキストが空文字の場合_ErrEmptyContent が返ること": {
			req:  Request{ModelID: "m", Messages: []Message{UserText("")}},
			want: ErrEmptyContent,
		},
		"異常系_画像のバイト列が空の場合_ErrEmptyContent が返ること": {
			req:  Request{ModelID: "m", Messages: []Message{UserImage(ImageFormatPNG, nil, "p")}},
			want: ErrEmptyContent,
		},
		"異常系_content block が 1 つもない場合_ErrEmptyContent が返ること": {
			req:  Request{ModelID: "m", Messages: []Message{{Role: RoleUser}}},
			want: ErrEmptyContent,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{}
			c := New(api)
			_, err := c.Converse(context.Background(), tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if api.calls() != 0 {
				t.Errorf("api calls = %d, want 0", api.calls())
			}
		})
	}
}

func TestConverseNoTextContent(t *testing.T) {
	api := &fakeAPI{outputs: []*awsbedrockruntime.ConverseOutput{{
		Output:     &awsbedrockruntimetypes.ConverseOutputMemberMessage{Value: awsbedrockruntimetypes.Message{}},
		StopReason: awsbedrockruntimetypes.StopReasonEndTurn,
	}}}
	c := New(api, WithDefaultModelID("m"))

	if _, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}}); !errors.Is(err, ErrNoTextContent) {
		t.Fatalf("err = %v, want ErrNoTextContent", err)
	}
}

func TestResponseDecodeJSON(t *testing.T) {
	tests := map[string]struct {
		text      string
		wantTitle string
		wantErr   bool
	}{
		"正常系_JSON のみの応答の場合_そのまま読み込めること":          {text: `{"title":"a"}`, wantTitle: "a"},
		"正常系_言語指定つきコードフェンスの場合_フェンスを外して読み込めること":   {text: "```json\n{\"title\":\"b\"}\n```", wantTitle: "b"},
		"正常系_言語指定なしコードフェンスの場合_フェンスを外して読み込めること":   {text: "```\n{\"title\":\"c\"}\n```", wantTitle: "c"},
		"正常系_前後に散文がある場合_JSON 部分だけ切り出せること":        {text: "以下が結果です\n{\"title\":\"d\"}\nご確認ください", wantTitle: "d"},
		"異常系_JSON が含まれない場合_ErrInvalidJSON が返ること": {text: "申し訳ありませんが構造化できませんでした", wantErr: true},
		"異常系_JSON が壊れている場合_ErrInvalidJSON が返ること": {text: `{"title":}`, wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var got struct {
				Title string `json:"title"`
			}
			err := (&Response{Text: tt.text}).DecodeJSON(&got)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidJSON) {
					t.Fatalf("err = %v, want ErrInvalidJSON", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeJSON: %v", err)
			}
			if got.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tt.wantTitle)
			}
		})
	}
}
