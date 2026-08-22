package bedrockparser

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/bedrockroute"
)

const (
	testBucket    = "test-folio-documents"
	testJobID     = "job-0001"
	samplePaperID = "2301.07041"
	sampleModelID = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
)

// samplePNG は画像の中身を検証しないテストで用いるダミーのバイト列
var samplePNG = []byte{0x89, 'P', 'N', 'G'}

// testdataDir は記録済みレスポンスの配置先 (規約上 backend/testdata/bedrock)
func testdataDir() string {
	return filepath.Join("..", "..", "..", "testdata", "bedrock")
}

// spyConverser は Request を控えつつ next へ委ねる
//
// ハンドラは本番と同じく RecordKey を空のまま渡すため、Replayer が要求するキーはここで補う
type spyConverser struct {
	next      bedrock.Converser
	recordKey string
	reqs      []bedrock.Request
}

var _ bedrock.Converser = (*spyConverser)(nil)

func (s *spyConverser) Converse(ctx context.Context, req bedrock.Request) (*bedrock.Response, error) {
	s.reqs = append(s.reqs, req)
	req.RecordKey = s.recordKey
	return s.next.Converse(ctx, req)
}

// stubConverser は決まったテキストかエラーを返す (実 API は一切呼ばない)
type stubConverser struct {
	text string
	err  error
}

var _ bedrock.Converser = (*stubConverser)(nil)

func (s *stubConverser) Converse(context.Context, bedrock.Request) (*bedrock.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &bedrock.Response{Text: s.text, StopReason: "end_turn"}, nil
}

// steppingClock は呼ぶたびに step ずつ進む時計を返す (DurationMs を決定的に検証するため)
func steppingClock(start time.Time, step time.Duration) func() time.Time {
	t := start.Add(-step)
	return func() time.Time {
		t = t.Add(step)
		return t
	}
}

// newTestHandler はフェイクの S3 と与えられた Converser でハンドラを組み立てる
func newTestHandler(t *testing.T, conv bedrock.Converser, opts ...Option) (*Handler, *s3test.Fake) {
	t.Helper()

	fake := s3test.NewFake()
	h := New(s3.New(fake, testBucket), bedrockroute.New(conv, sampleModelID), opts...)
	return h, fake
}

// assertNoResultSaved はページ結果が保存されていないことを確かめる (失敗した反復の結果を後段が拾わないため)
func assertNoResultSaved(t *testing.T, fake *s3test.Fake, page int) {
	t.Helper()

	if _, ok := fake.Object(testBucket, s3.BedrockPageResultKey(testJobID, page)); ok {
		t.Errorf("失敗したのにページ結果が保存されている: %s", s3.BedrockPageResultKey(testJobID, page))
	}
}

// 記録済みレスポンスを再生し、1 ページ入力に対して 1 ページ分の封筒が保存されキーだけが返ることを確かめる
//
// 2301.07041-bedrock.json は 1 ページ分 ("page":1 と表紙の書誌情報のみ) の合成フィクスチャであり、1 ページ目の入力として扱う
func TestHandleReplaysRecording(t *testing.T) {
	spy := &spyConverser{
		next:      bedrock.NewReplayer(testdataDir()),
		recordKey: bedrock.RecordKey(samplePaperID, bedrock.RouteBedrock),
	}
	start := time.Date(2026, 8, 16, 12, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	step := 1500 * time.Millisecond
	h, fake := newTestHandler(t, spy, WithClock(steppingClock(start, step)))
	fake.Seed(testBucket, s3.PageImageKey(testJobID, 1), s3test.Object{Body: samplePNG, ContentType: "image/png"})

	got, err := h.Handle(context.Background(), Input{JobID: testJobID, Page: 1, Language: domain.LanguageEnglish})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got == nil {
		t.Fatal("Handle() = nil, want output")
	}

	want := Output{JobID: testJobID, Page: 1, ResultKey: "work/" + testJobID + "/bedrock/page-0001.json"}
	if *got != want {
		t.Errorf("Handle() = %+v, want %+v", got, want)
	}

	// 出力は S3 キーだけを載せ、抽出結果の実体を含めない (State 間の上限 256KB)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if strings.Contains(string(encoded), "Learning to Structure") {
		t.Errorf("出力に抽出結果の実体が含まれている: %s", encoded)
	}

	obj, ok := fake.Object(testBucket, want.ResultKey)
	if !ok {
		t.Fatalf("ページ結果が保存されていない: %s", want.ResultKey)
	}
	if obj.ContentType != s3.ContentTypeJSON {
		t.Errorf("ContentType = %q, want %q", obj.ContentType, s3.ContentTypeJSON)
	}

	var envelope PageOutput
	if err := json.Unmarshal(obj.Body, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.JobID != testJobID || envelope.Page != 1 {
		t.Errorf("封筒の jobId/page = %q/%d, want %q/1", envelope.JobID, envelope.Page, testJobID)
	}
	// 時計は画像取得の前と抽出完了の後の 2 回進むため、ExtractedAt は 2 回目、DurationMs は 1 歩分になる
	if wantAt := start.Add(step); !envelope.ExtractedAt.Equal(wantAt) {
		t.Errorf("ExtractedAt = %v, want %v", envelope.ExtractedAt, wantAt)
	}
	if envelope.ExtractedAt.Location() != time.UTC {
		t.Errorf("ExtractedAt のタイムゾーン = %v, want UTC", envelope.ExtractedAt.Location())
	}
	if envelope.DurationMs != step.Milliseconds() {
		t.Errorf("DurationMs = %d, want %d", envelope.DurationMs, step.Milliseconds())
	}

	result := envelope.Result
	if result.Page != 1 {
		t.Errorf("Result.Page = %d, want 1", result.Page)
	}
	if result.Title != "Learning to Structure Scientific Documents" {
		t.Errorf("Result.Title = %q", result.Title)
	}
	if result.ModelID != sampleModelID {
		t.Errorf("Result.ModelID = %q, want %q", result.ModelID, sampleModelID)
	}
	// Usage は後段が provenance.cost へ合算するため、封筒を経由しても失われない必要がある
	if result.Usage.InputTokens != 2317 || result.Usage.OutputTokens != 264 {
		t.Errorf("Result.Usage = %+v, want input=2317 output=264", result.Usage)
	}

	// Bedrock の呼び出しは 1 回だけで、取得したページ画像と言語ヒントがそのまま渡り、RecordKey は本番と同じく空であること
	if len(spy.reqs) != 1 {
		t.Fatalf("Converse の呼び出し回数 = %d, want 1", len(spy.reqs))
	}
	req := spy.reqs[0]
	if req.RecordKey != "" {
		t.Errorf("RecordKey = %q, want 空 (本番の呼び出しでは記録しない)", req.RecordKey)
	}
	img, ok := req.Messages[0].Content[0].(bedrock.ImagePart)
	if !ok || string(img.Bytes) != string(samplePNG) {
		t.Errorf("ページ画像が渡っていない: %+v", req.Messages[0].Content[0])
	}
	text, ok := req.Messages[0].Content[1].(bedrock.TextPart)
	if !ok || !strings.Contains(text.Text, `"en"`) {
		t.Errorf("言語ヒントが渡っていない: %+v", req.Messages[0].Content[1])
	}
}

// ページ番号が画像の取得元と結果の保存先の両方に反映されることを確かめる (記録は 1 ページ目のみのためスタブで代える)
func TestHandleDerivesKeysFromPage(t *testing.T) {
	h, fake := newTestHandler(t, &stubConverser{text: `{"page":1,"sections":[{"text":"続き"}]}`})
	fake.Seed(testBucket, s3.PageImageKey(testJobID, 3), s3test.Object{Body: samplePNG})

	got, err := h.Handle(context.Background(), Input{JobID: testJobID, Page: 3})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got == nil {
		t.Fatal("Handle() = nil, want output")
	}
	if want := s3.BedrockPageResultKey(testJobID, 3); got.ResultKey != want {
		t.Errorf("ResultKey = %q, want %q", got.ResultKey, want)
	}

	obj, ok := fake.Object(testBucket, got.ResultKey)
	if !ok {
		t.Fatalf("ページ結果が保存されていない: %s", got.ResultKey)
	}
	var envelope PageOutput
	if err := json.Unmarshal(obj.Body, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	// モデルの申告 ("page":1) ではなく入力のページ番号で埋まっていること
	if envelope.Page != 3 || envelope.Result.Page != 3 {
		t.Errorf("封筒の page = %d, Result.Page = %d, want 3/3", envelope.Page, envelope.Result.Page)
	}
	if !envelope.ExtractedAt.After(time.Time{}) || envelope.DurationMs < 0 {
		t.Errorf("ExtractedAt/DurationMs が埋まっていない: %v/%d", envelope.ExtractedAt, envelope.DurationMs)
	}
}

// 再試行しても解消しない入力は InvalidInputError で返り、課金を伴う呼び出しと保存が起きないことを確かめる
func TestHandleInvalidInput(t *testing.T) {
	tests := map[string]struct {
		in    Input
		image []byte // image は nil なら配置しない
		want  error
	}{
		"異常系_jobId が空の場合_ErrEmptyJobID を包んで返ること":             {in: Input{Page: 1}, want: ErrEmptyJobID},
		"境界値_ページ番号が 0 の場合_ErrInvalidPage を包んで返ること":           {in: Input{JobID: testJobID, Page: 0}, want: ErrInvalidPage},
		"境界値_ページ番号が負の場合_ErrInvalidPage を包んで返ること":             {in: Input{JobID: testJobID, Page: -1}, want: ErrInvalidPage},
		"異常系_ページ画像が無い場合_s3.ErrNotFound を包んで返ること":             {in: Input{JobID: testJobID, Page: 2}, want: s3.ErrNotFound},
		"異常系_ページ画像が空の場合_bedrockroute.ErrEmptyImage を包んで返ること": {in: Input{JobID: testJobID, Page: 2}, image: []byte{}, want: bedrockroute.ErrEmptyImage},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spy := &spyConverser{next: &stubConverser{text: "{}"}}
			h, fake := newTestHandler(t, spy)
			if tt.image != nil {
				fake.Seed(testBucket, s3.PageImageKey(tt.in.JobID, tt.in.Page), s3test.Object{Body: tt.image})
			}

			// Lambda は最外の型名を errorType として報告するため、包まれていない最外の型で判定する
			got, err := h.Handle(context.Background(), tt.in)
			if _, ok := err.(*InvalidInputError); !ok {
				t.Fatalf("err = %T (%v), want *InvalidInputError", err, err)
			}
			if got != nil {
				t.Errorf("Handle() = %+v, want nil", got)
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v を包むこと", err, tt.want)
			}
			if len(spy.reqs) != 0 {
				t.Errorf("Converse の呼び出し回数 = %d, want 0 (課金を伴う呼び出しをしてはならない)", len(spy.reqs))
			}
			assertNoResultSaved(t, fake, tt.in.Page)
		})
	}
}

// 解釈できない応答は PageDecodeError で返り、由来のセンチネルも辿れ、生応答が error.json に残ることを確かめる
func TestHandleDecodeFailure(t *testing.T) {
	const reply = "この画像からは読み取れませんでした"
	saveErr := errors.New("put failed")

	tests := map[string]struct {
		putErr   error
		wantDump bool
	}{
		"異常系_解釈できない応答の場合_生応答が error.json に残ること":            {wantDump: true},
		"異常系_生応答の保存に失敗した場合_PageDecodeError のまま保存の失敗も辿れること": {putErr: saveErr},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			start := time.Date(2026, 8, 22, 23, 9, 0, 0, time.UTC)
			step := time.Second
			h, fake := newTestHandler(t, &stubConverser{text: reply}, WithClock(steppingClock(start, step)))
			fake.Seed(testBucket, s3.PageImageKey(testJobID, 1), s3test.Object{Body: samplePNG})
			fake.PutErr = tt.putErr

			got, err := h.Handle(context.Background(), Input{JobID: testJobID, Page: 1})
			if _, ok := err.(*PageDecodeError); !ok {
				t.Fatalf("err = %T (%v), want *PageDecodeError (最外の型名が errorType になる)", err, err)
			}
			if got != nil {
				t.Errorf("Handle() = %+v, want nil", got)
			}
			if !errors.Is(err, bedrock.ErrInvalidJSON) || !errors.Is(err, bedrockroute.ErrPageDecode) {
				t.Errorf("err = %v, want bedrock.ErrInvalidJSON と bedrockroute.ErrPageDecode を包むこと", err)
			}
			assertNoResultSaved(t, fake, 1)

			key := s3.BedrockPageErrorKey(testJobID, 1)
			obj, ok := fake.Object(testBucket, key)
			if !tt.wantDump {
				if ok {
					t.Errorf("保存に失敗したのに生応答がある: %s", key)
				}
				if !errors.Is(err, saveErr) {
					t.Errorf("err = %v, want 保存の失敗 %v も包むこと", err, saveErr)
				}
				return
			}
			if !ok {
				t.Fatalf("生応答が保存されていない: %s", key)
			}
			var dump PageError
			if err := json.Unmarshal(obj.Body, &dump); err != nil {
				t.Fatalf("unmarshal error dump: %v", err)
			}
			if dump.JobID != testJobID || dump.Page != 1 {
				t.Errorf("生応答の jobId/page = %q/%d, want %q/1", dump.JobID, dump.Page, testJobID)
			}
			if dump.Response.Text != reply || dump.Response.StopReason != "end_turn" {
				t.Errorf("生応答の response = %+v, want モデルの応答をそのまま残すこと", dump.Response)
			}
			if !strings.Contains(dump.Error, "page 1") {
				t.Errorf("生応答の error = %q, want 失敗の理由を含むこと", dump.Error)
			}
			// 時計は画像取得の前と保存の直前の 2 回進む
			if wantAt := start.Add(step); !dump.FailedAt.Equal(wantAt) {
				t.Errorf("FailedAt = %v, want %v", dump.FailedAt, wantAt)
			}
		})
	}
}

// スロットリングなど抽出側の失敗は型を付け替えずそのまま返し、Retry の対象に残ることを確かめる
func TestHandlePropagatesExtractError(t *testing.T) {
	want := errors.New("throttled")
	h, fake := newTestHandler(t, &stubConverser{err: want})
	fake.Seed(testBucket, s3.PageImageKey(testJobID, 1), s3test.Object{Body: samplePNG})

	got, err := h.Handle(context.Background(), Input{JobID: testJobID, Page: 1})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if got != nil {
		t.Errorf("Handle() = %+v, want nil", got)
	}
	if _, ok := errors.AsType[*InvalidInputError](err); ok {
		t.Errorf("err = %v, InvalidInputError であってはならない", err)
	}
	if _, ok := errors.AsType[*PageDecodeError](err); ok {
		t.Errorf("err = %v, PageDecodeError であってはならない", err)
	}
	assertNoResultSaved(t, fake, 1)
}

// 出力にページ数や要素数に比例して伸びるフィールドを持たせない (State 間の上限 256KB)
func TestOutputHasNoCollectionFields(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Output]()
	for field := range typ.Fields() {
		switch field.Type.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
			t.Errorf("Output.%s は %s であり実体を抱え込みうる", field.Name, field.Type.Kind())
		}
	}
}
