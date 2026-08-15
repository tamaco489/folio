package bedrock

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// fakeAPI は ConverseAPI のフェイク。実 API は一切呼ばない
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
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}},
			},
		},
		StopReason: types.StopReasonEndTurn,
		Usage: &types.TokenUsage{
			InputTokens:  aws.Int32(1200),
			OutputTokens: aws.Int32(340),
			TotalTokens:  aws.Int32(1540),
		},
		Metrics: &types.ConverseMetrics{LatencyMs: aws.Int64(4321)},
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
	text, ok := content[0].(*types.ContentBlockMemberText)
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
	img, ok := content[0].(*types.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("content[0] = %T, want image block", content[0])
	}
	if img.Value.Format != types.ImageFormatPng {
		t.Errorf("format = %q, want png", img.Value.Format)
	}
	src, ok := img.Value.Source.(*types.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("source = %T, want bytes", img.Value.Source)
	}
	if string(src.Value) != string(png) {
		t.Errorf("image bytes = %v", src.Value)
	}
	if _, ok := content[1].(*types.ContentBlockMemberText); !ok {
		t.Fatalf("content[1] = %T, want text block", content[1])
	}
}

func TestConverseValidation(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want error
	}{
		{
			name: "model id missing",
			req:  Request{Messages: []Message{UserText("x")}},
			want: ErrModelIDRequired,
		},
		{
			name: "no messages",
			req:  Request{ModelID: "m"},
			want: ErrEmptyRequest,
		},
		{
			name: "empty text",
			req:  Request{ModelID: "m", Messages: []Message{UserText("")}},
			want: ErrEmptyContent,
		},
		{
			name: "empty image",
			req:  Request{ModelID: "m", Messages: []Message{UserImage(ImageFormatPNG, nil, "p")}},
			want: ErrEmptyContent,
		},
		{
			name: "no content",
			req:  Request{ModelID: "m", Messages: []Message{{Role: RoleUser}}},
			want: ErrEmptyContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
		Output:     &types.ConverseOutputMemberMessage{Value: types.Message{}},
		StopReason: types.StopReasonEndTurn,
	}}}
	c := New(api, WithDefaultModelID("m"))

	if _, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}}); !errors.Is(err, ErrNoTextContent) {
		t.Fatalf("err = %v, want ErrNoTextContent", err)
	}
}

func TestResponseDecodeJSON(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantTitle string
		wantErr   bool
	}{
		{name: "bare json", text: `{"title":"a"}`, wantTitle: "a"},
		{name: "fenced json", text: "```json\n{\"title\":\"b\"}\n```", wantTitle: "b"},
		{name: "fenced without lang", text: "```\n{\"title\":\"c\"}\n```", wantTitle: "c"},
		{name: "prose around json", text: "以下が結果です\n{\"title\":\"d\"}\nご確認ください", wantTitle: "d"},
		{name: "no json at all", text: "申し訳ありませんが構造化できませんでした", wantErr: true},
		{name: "broken json", text: `{"title":}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
