package bedrock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// testdataDir は justfile が定める記録の配置先
func testdataDir() string {
	return filepath.Join("..", "..", "..", "testdata", "bedrock")
}

const samplePaperID = "2301.07041"

// 記録済みレスポンスを再生し、実 API を呼ばずに構造化 JSON まで取り出せることを確かめる
func TestReplayerReplaysRecordedResponses(t *testing.T) {
	tests := map[string]struct {
		route     Route
		wantInput int32
	}{
		"正常系_経路 A の記録の場合_実 API を呼ばずに構造化 JSON まで取り出せること": {route: RouteTextract, wantInput: 4821},
		"正常系_経路 B の記録の場合_実 API を呼ばずに構造化 JSON まで取り出せること": {route: RouteBedrock, wantInput: 2317},
	}

	replayer := NewReplayer(testdataDir())

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp, err := replayer.Converse(context.Background(), Request{
				RecordKey: RecordKey(samplePaperID, tt.route),
			})
			if err != nil {
				t.Fatalf("Converse: %v", err)
			}
			if resp.Usage.InputTokens != tt.wantInput {
				t.Errorf("InputTokens = %d, want %d", resp.Usage.InputTokens, tt.wantInput)
			}
			if resp.Usage.TotalTokens != resp.Usage.InputTokens+resp.Usage.OutputTokens {
				t.Errorf("TotalTokens = %d, does not match input+output", resp.Usage.TotalTokens)
			}
			if resp.StopReason != "tool_use" {
				t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
			}
			if len(resp.ToolInput) == 0 {
				t.Fatal("ToolInput が記録から読めていない")
			}

			// domain の型に依存しないよう、汎用の map で構造だけ確かめる
			var got map[string]any
			if err := resp.DecodeJSON(&got); err != nil {
				t.Fatalf("DecodeJSON: %v", err)
			}
			if got["title"] != "Learning to Structure Scientific Documents" {
				t.Errorf("title = %v", got["title"])
			}
		})
	}
}

func TestReplayerErrors(t *testing.T) {
	replayer := NewReplayer(testdataDir())

	if _, err := replayer.Converse(context.Background(), Request{}); !errors.Is(err, ErrRecordKeyRequired) {
		t.Errorf("err = %v, want ErrRecordKeyRequired", err)
	}
	if _, err := replayer.Converse(context.Background(), Request{RecordKey: "0000.00000-textract"}); !errors.Is(err, ErrRecordingNotFound) {
		t.Errorf("err = %v, want ErrRecordingNotFound", err)
	}
}

// 記録モードが応答をファイルに残し、再生モードで読み戻せることを確かめる
func TestRecorderRoundTrip(t *testing.T) {
	tests := map[string]struct {
		out       *awsbedrockruntime.ConverseOutput
		wantTitle string
	}{
		"正常系_自由文の応答の場合_Text が記録され再生で読み戻せること":            {out: textOutput(`{"title":"recorded"}`), wantTitle: "recorded"},
		"正常系_tool use の応答の場合_ToolInput が記録され再生で読み戻せること": {out: toolUseOutput(map[string]any{"title": "recorded by tool"}), wantTitle: "recorded by tool"},
		"正常系_tool use の応答に引用符が含まれる場合_記録を経ても引用符が壊れないこと":  {out: toolUseOutput(map[string]any{"title": `the word "Beauty"`}), wantTitle: `the word "Beauty"`},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			api := &fakeAPI{outputs: []*awsbedrockruntime.ConverseOutput{tt.out}}
			live := New(api, WithDefaultModelID("m"))

			recorder := NewRecorder(live, dir, RouteBedrock)
			recorder.now = func() time.Time { return time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC) }

			key := RecordKey("9999.99999", RouteBedrock)
			got, err := recorder.Converse(context.Background(), Request{
				ModelID:   "m",
				RecordKey: key,
				Messages:  []Message{UserImage(ImageFormatPNG, []byte{0x89, 'P'}, "read")},
				Tool:      sampleTool,
			})
			if err != nil {
				t.Fatalf("Converse: %v", err)
			}

			replayed, err := NewReplayer(dir).Converse(context.Background(), Request{RecordKey: key})
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			if replayed.Text != got.Text {
				t.Errorf("text = %q, want %q", replayed.Text, got.Text)
			}
			// 記録は整形して書くため、バイト列ではなく JSON として同じことを確かめる
			if len(got.ToolInput) > 0 && compactJSON(t, replayed.ToolInput) != compactJSON(t, got.ToolInput) {
				t.Errorf("toolInput = %s, want %s", replayed.ToolInput, got.ToolInput)
			}
			if replayed.Usage != got.Usage {
				t.Errorf("usage = %+v, want %+v", replayed.Usage, got.Usage)
			}
			if replayed.LatencyMs != got.LatencyMs {
				t.Errorf("latencyMs = %d, want %d", replayed.LatencyMs, got.LatencyMs)
			}

			var decoded struct {
				Title string `json:"title"`
			}
			if err := replayed.DecodeJSON(&decoded); err != nil {
				t.Fatalf("DecodeJSON: %v", err)
			}
			if decoded.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", decoded.Title, tt.wantTitle)
			}

			rec, err := NewStore(dir).Load(key)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if rec.Route != RouteBedrock || rec.ModelID != "m" {
				t.Errorf("recording metadata = %+v", rec)
			}
			if rec.RecordedAt.IsZero() {
				t.Error("RecordedAt が記録されていない")
			}
			// 再生時に tool use の記録が自由文の経路へ落ちないよう、text を持たないことも確かめる
			if len(got.ToolInput) > 0 && rec.Response.Text != "" {
				t.Errorf("text = %q, want 空", rec.Response.Text)
			}
		})
	}
}

func TestRecorderRequiresKey(t *testing.T) {
	api := &fakeAPI{}
	recorder := NewRecorder(New(api, WithDefaultModelID("m")), t.TempDir(), RouteTextract)

	if _, err := recorder.Converse(context.Background(), Request{Messages: []Message{UserText("x")}}); !errors.Is(err, ErrRecordKeyRequired) {
		t.Fatalf("err = %v, want ErrRecordKeyRequired", err)
	}
	if api.calls() != 0 {
		t.Errorf("api calls = %d, want 0 (課金を伴う呼び出しをしてはならない)", api.calls())
	}
}

func TestRecordKey(t *testing.T) {
	if got := RecordKey("2301.07041", RouteTextract); got != "2301.07041-textract" {
		t.Errorf("RecordKey = %q", got)
	}
}
