package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awsbedrockruntimedocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
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
	return output(awsbedrockruntimetypes.StopReasonEndTurn, &awsbedrockruntimetypes.ContentBlockMemberText{Value: text})
}

// toolUseOutput は tool use を強制した応答を模す (input は SDK が受信時に組み立てる document と同じ経路で JSON になる)
func toolUseOutput(input any) *awsbedrockruntime.ConverseOutput {
	return output(awsbedrockruntimetypes.StopReasonToolUse, &awsbedrockruntimetypes.ContentBlockMemberToolUse{
		Value: awsbedrockruntimetypes.ToolUseBlock{
			ToolUseId: new("tooluse_01"),
			Name:      new("extract_page"),
			Input:     awsbedrockruntimedocument.NewLazyDocument(input),
		},
	})
}

func output(stop awsbedrockruntimetypes.StopReason, blocks ...awsbedrockruntimetypes.ContentBlock) *awsbedrockruntime.ConverseOutput {
	return &awsbedrockruntime.ConverseOutput{
		Output: &awsbedrockruntimetypes.ConverseOutputMemberMessage{
			Value: awsbedrockruntimetypes.Message{
				Role:    awsbedrockruntimetypes.ConversationRoleAssistant,
				Content: blocks,
			},
		},
		StopReason: stop,
		Usage: &awsbedrockruntimetypes.TokenUsage{
			InputTokens:  new(int32(1200)),
			OutputTokens: new(int32(340)),
			TotalTokens:  new(int32(1540)),
		},
		Metrics: &awsbedrockruntimetypes.ConverseMetrics{LatencyMs: new(int64(4321))},
	}
}

// sampleTool は buildInput の検証に用いる最小の tool 定義
var sampleTool = &ToolSpec{
	Name:        "extract_page",
	Description: "Record the structured content of one page.",
	Schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
		},
	},
}

func TestConverseTextInput(t *testing.T) {
	api := &fakeAPI{outputs: []*awsbedrockruntime.ConverseOutput{textOutput(`{"title":"ok"}`)}}
	c := New(api, WithDefaultModelID("model-from-config"))

	resp, err := c.Converse(context.Background(), Request{
		System:      "structure the document",
		Messages:    []Message{UserText("PAGE 1 ...")},
		MaxTokens:   new(int32(4096)),
		Temperature: new(float32(0)),
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
		"異常系_tool の名前が空の場合_ErrInvalidToolSpec が返ること": {
			req:  Request{ModelID: "m", Messages: []Message{UserText("x")}, Tool: &ToolSpec{Schema: sampleTool.Schema}},
			want: ErrInvalidToolSpec,
		},
		"異常系_tool のスキーマが無い場合_ErrInvalidToolSpec が返ること": {
			req:  Request{ModelID: "m", Messages: []Message{UserText("x")}, Tool: &ToolSpec{Name: "extract_page"}},
			want: ErrInvalidToolSpec,
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

// Request.Tool が ConverseInput の toolConfig に落ち、toolChoice でその tool が強制されることを確かめる
func TestConverseToolConfig(t *testing.T) {
	api := &fakeAPI{outputs: []*awsbedrockruntime.ConverseOutput{toolUseOutput(map[string]any{"title": "ok"})}}
	c := New(api, WithDefaultModelID("m"))

	if _, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}, Tool: sampleTool}); err != nil {
		t.Fatalf("Converse: %v", err)
	}

	tc := api.inputs[0].ToolConfig
	if tc == nil {
		t.Fatal("ToolConfig が設定されていない")
	}
	if len(tc.Tools) != 1 {
		t.Fatalf("Tools = %d 件, want 1", len(tc.Tools))
	}
	spec, ok := tc.Tools[0].(*awsbedrockruntimetypes.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("Tools[0] = %T, want ToolMemberToolSpec", tc.Tools[0])
	}
	if aws.ToString(spec.Value.Name) != "extract_page" || aws.ToString(spec.Value.Description) != sampleTool.Description {
		t.Errorf("ToolSpecification = name %q, description %q", aws.ToString(spec.Value.Name), aws.ToString(spec.Value.Description))
	}
	schema, ok := spec.Value.InputSchema.(*awsbedrockruntimetypes.ToolInputSchemaMemberJson)
	if !ok {
		t.Fatalf("InputSchema = %T, want ToolInputSchemaMemberJson", spec.Value.InputSchema)
	}
	raw, err := schema.Value.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("MarshalSmithyDocument: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("schema is not json: %v", err)
	}
	if got["type"] != "object" {
		t.Errorf("schema = %s", raw)
	}
	choice, ok := tc.ToolChoice.(*awsbedrockruntimetypes.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("ToolChoice = %T, want ToolChoiceMemberTool", tc.ToolChoice)
	}
	if aws.ToString(choice.Value.Name) != "extract_page" {
		t.Errorf("ToolChoice.Name = %q, want extract_page", aws.ToString(choice.Value.Name))
	}
}

// Tool を指定しない Request には toolConfig を付けないことを確かめる (tool 非対応のモデルでも従来どおり呼べる)
func TestConverseWithoutToolConfig(t *testing.T) {
	api := &fakeAPI{}
	c := New(api, WithDefaultModelID("m"))

	if _, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}}); err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if api.inputs[0].ToolConfig != nil {
		t.Errorf("ToolConfig = %+v, want nil", api.inputs[0].ToolConfig)
	}
}

// toolUse content block の input が ToolInput に JSON として入ることを確かめる
func TestConverseToolUseResponse(t *testing.T) {
	tests := map[string]struct {
		out       *awsbedrockruntime.ConverseOutput
		wantInput string
		wantText  string
	}{
		"正常系_toolUse だけの応答の場合_input が ToolInput に入り Text は空になること": {
			out:       toolUseOutput(map[string]any{"title": "ok", "year": 2026}),
			wantInput: `{"title":"ok","year":2026}`,
		},
		"正常系_テキストと toolUse が並ぶ応答の場合_両方が取れること": {
			out: output(awsbedrockruntimetypes.StopReasonToolUse,
				&awsbedrockruntimetypes.ContentBlockMemberText{Value: "I will record the page."},
				&awsbedrockruntimetypes.ContentBlockMemberToolUse{Value: awsbedrockruntimetypes.ToolUseBlock{
					ToolUseId: new("tooluse_02"),
					Name:      new("extract_page"),
					Input:     awsbedrockruntimedocument.NewLazyDocument(map[string]any{"title": "ok"}),
				}},
			),
			wantInput: `{"title":"ok"}`,
			wantText:  "I will record the page.",
		},
		"正常系_本文に引用符を含む応答の場合_エスケープされた JSON として ToolInput に入ること": {
			out:       toolUseOutput(map[string]any{"text": `the word "Beauty" appears`}),
			wantInput: `{"text":"the word \"Beauty\" appears"}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{outputs: []*awsbedrockruntime.ConverseOutput{tt.out}}
			c := New(api, WithDefaultModelID("m"))

			resp, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}, Tool: sampleTool})
			if err != nil {
				t.Fatalf("Converse: %v", err)
			}
			if got := compactJSON(t, resp.ToolInput); got != tt.wantInput {
				t.Errorf("ToolInput = %s, want %s", got, tt.wantInput)
			}
			if resp.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", resp.Text, tt.wantText)
			}
			if resp.StopReason != "tool_use" {
				t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
			}
		})
	}
}

// compactJSON はキー順と空白を揃えて比較できる形にする
func compactJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not json: %v (%s)", err, raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestResponseDecodeJSON(t *testing.T) {
	tests := map[string]struct {
		text       string
		toolInput  string
		stopReason string
		wantTitle  string
		wantErr    error
	}{
		"正常系_JSON のみの応答の場合_そのまま読み込めること":                                         {text: `{"title":"a"}`, wantTitle: "a"},
		"正常系_言語指定つきコードフェンスの場合_フェンスを外して読み込めること":                                  {text: "```json\n{\"title\":\"b\"}\n```", wantTitle: "b"},
		"正常系_言語指定なしコードフェンスの場合_フェンスを外して読み込めること":                                  {text: "```\n{\"title\":\"c\"}\n```", wantTitle: "c"},
		"正常系_前後に散文がある場合_JSON 部分だけ切り出せること":                                       {text: "以下が結果です\n{\"title\":\"d\"}\nご確認ください", wantTitle: "d"},
		"正常系_ToolInput がある場合_それを読み込むこと":                                         {toolInput: `{"title":"g"}`, stopReason: "tool_use", wantTitle: "g"},
		"正常系_ToolInput とテキストの両方がある場合_ToolInput を優先すること":                         {text: `{"title":"text"}`, toolInput: `{"title":"tool"}`, wantTitle: "tool"},
		"正常系_ToolInput の文字列に引用符が含まれる場合_壊れずに復元できること":                             {toolInput: `{"title":"the word \"Beauty\" appears"}`, wantTitle: `the word "Beauty" appears`},
		"異常系_JSON が含まれない場合_ErrInvalidJSON が返ること":                                {text: "申し訳ありませんが構造化できませんでした", wantErr: ErrInvalidJSON},
		"異常系_JSON が壊れている場合_ErrInvalidJSON が返ること":                                {text: `{"title":}`, wantErr: ErrInvalidJSON},
		"異常系_ToolInput が壊れている場合_テキストへ落ちず ErrInvalidJSON が返ること":                  {text: `{"title":"text"}`, toolInput: `{"title":}`, wantErr: ErrInvalidJSON},
		"異常系_max_tokens で打ち切られた場合_ErrInvalidJSON ではなく ErrOutputTruncated が返ること": {text: `{"title":"e","sections":[{"heading":"1 Intro`, stopReason: StopReasonMaxTokens, wantErr: ErrOutputTruncated},
		"境界値_max_tokens で打ち切られたが JSON として読める場合_ErrOutputTruncated が返ること":        {text: `{"title":"f"}`, stopReason: StopReasonMaxTokens, wantErr: ErrOutputTruncated},
		"境界値_max_tokens で打ち切られたが ToolInput がある場合_ErrOutputTruncated が返ること":      {toolInput: `{"title":"h"}`, stopReason: StopReasonMaxTokens, wantErr: ErrOutputTruncated},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var got struct {
				Title string `json:"title"`
			}
			err := (&Response{Text: tt.text, ToolInput: json.RawMessage(tt.toolInput), StopReason: tt.stopReason}).DecodeJSON(&got)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
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
