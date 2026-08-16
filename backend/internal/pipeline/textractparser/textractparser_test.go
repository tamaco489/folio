package textractparser

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
	"github.com/tamaco489/folio/backend/internal/awsx/sfn"
	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/textractroute"
)

// 記録済みレスポンスの置き場所 (backend/testdata)。Textract の記録は手書きの合成データである点に注意
const (
	textractRecording = "../../../testdata/textract/1706.03762.json"
	bedrockDir        = "../../../testdata/bedrock"
	recordedModelID   = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
)

const (
	testBucket = "test-folio-documents"
	// jobID は Bedrock の記録 2301.07041-textract.json を再生に使うため論文 ID に揃える (PaperID には jobId が渡る)
	jobID     = "2301.07041"
	taskToken = "AQCEAAAAKgAAAAMAAAAAAAAAAexample-task-token"
	topicARN  = "arn:aws:sns:us-east-1:000000000000:dev-folio-textract-completion"
	roleARN   = "arn:aws:iam::000000000000:role/dev-folio-textract-publish-role"
)

var (
	uploadedAt = time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	startedAt  = time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	finishedAt = startedAt.Add(90 * time.Second)

	featureTypes = []types.FeatureType{types.FeatureTypeLayout, types.FeatureTypeTables}
)

// env はフェイクと再生だけで組み立てたハンドラ一式
type env struct {
	handler  *Handler
	docs     *s3test.Fake
	textract *spyTextract
	states   *fakeStates
	rec      *textract.Recording
}

// newEnv はハンドラを組み立てる
//
// 時計は 1 回目の呼び出し (起動) で startedAt、2 回目以降 (完了) で finishedAt を返す
func newEnv(t *testing.T) *env {
	t.Helper()

	rec, err := textract.LoadRecording(textractRecording)
	if err != nil {
		t.Fatalf("LoadRecording: %v", err)
	}
	replayer, err := textract.NewReplayer(rec)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}

	e := &env{
		docs:     s3test.NewFake(),
		textract: &spyTextract{API: replayer},
		states:   &fakeStates{},
		rec:      rec,
	}
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return startedAt
		}
		return finishedAt
	}
	e.handler = New(
		s3.New(e.docs, testBucket),
		textract.New(e.textract),
		sfn.New(e.states),
		textractroute.New(bedrock.NewReplayer(bedrockDir), recordedModelID),
		Analysis{SNSTopicARN: topicARN, RoleARN: roleARN, FeatureTypes: featureTypes},
		WithClock(clock),
	)
	return e
}

func (e *env) seedPDF(t *testing.T) {
	t.Helper()
	e.docs.Seed(testBucket, s3.OriginalPDFKey(jobID), s3test.Object{Body: []byte("%PDF-1.7\n"), LastModified: uploadedAt})
}

// seedCallback は起動側が退避する情報を直接置く
func (e *env) seedCallback(t *testing.T, id, textractJobID string) Callback {
	t.Helper()
	cb := Callback{
		JobID:         id,
		TaskToken:     taskToken,
		TextractJobID: textractJobID,
		StartedAt:     startedAt,
		FeatureTypes:  featureTypes,
		Source: domain.Source{
			Bucket:     testBucket,
			Key:        s3.OriginalPDFKey(id),
			Filename:   s3.OriginalPDFKey(id),
			SHA256:     id,
			Language:   domain.LanguageEnglish,
			PageCount:  2,
			UploadedAt: uploadedAt,
		},
	}
	b, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	e.docs.Seed(testBucket, s3.TextractCallbackKey(id), s3test.Object{Body: b, ContentType: s3.ContentTypeJSON})
	return cb
}

func (e *env) object(t *testing.T, key string, v any) {
	t.Helper()
	obj, ok := e.docs.Object(testBucket, key)
	if !ok {
		t.Fatalf("%s が保存されていない", key)
	}
	if obj.ContentType != s3.ContentTypeJSON {
		t.Errorf("%s の ContentType = %q, want %q", key, obj.ContentType, s3.ContentTypeJSON)
	}
	if err := json.Unmarshal(obj.Body, v); err != nil {
		t.Fatalf("%s を復元できない: %v", key, err)
	}
}

func (e *env) assertAbsent(t *testing.T, key string) {
	t.Helper()
	if _, ok := e.docs.Object(testBucket, key); ok {
		t.Errorf("%s が保存されている", key)
	}
}

func startEvent(t *testing.T) json.RawMessage {
	t.Helper()
	// JSON タグ (lowerCamelCase) を含めて確かめるため構造体ではなく文字列で組み立てる
	return json.RawMessage(`{"jobId":"` + jobID + `","taskToken":"` + taskToken + `","language":"en","pageCount":2,"hasTextLayer":true}`)
}

// snsEvent は Textract の完了通知を SNS が Lambda に配送する形に包む
func snsEvent(t *testing.T, n textract.CompletionNotification) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	ev := events.SNSEvent{Records: []events.SNSEventRecord{{
		EventVersion:         "1.0",
		EventSubscriptionArn: topicARN + ":00000000-0000-0000-0000-000000000000",
		EventSource:          "aws:sns",
		SNS: events.SNSEntity{
			Type:      "Notification",
			MessageID: "95df01b4-ee98-5cb9-9903-4c221d41eb5e",
			TopicArn:  topicARN,
			Timestamp: finishedAt,
			Message:   string(body),
		},
	}}}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal sns event: %v", err)
	}
	return raw
}

func completion(id, textractJobID string, status types.JobStatus) textract.CompletionNotification {
	return textract.CompletionNotification{
		JobID:            textractJobID,
		Status:           status,
		API:              "StartDocumentAnalysis",
		JobTag:           id,
		Timestamp:        finishedAt.UnixMilli(),
		DocumentLocation: textract.NotifiedS3{Bucket: testBucket, Name: s3.OriginalPDFKey(id)},
	}
}

func assertSmall(t *testing.T, label string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", label, err)
	}
	// State 間の上限 256KB に対し S3 キーだけの出力は 1KB にも満たない
	if len(b) > 1024 {
		t.Errorf("%s = %d bytes, want 1KB 未満: %s", label, len(b), b)
	}
}

// 起動から完了通知までを通し、記録の再生だけで結果が S3 に揃い Step Functions へ応答することを確かめる
func TestHandleStartThenCallback(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedPDF(t)
	ctx := context.Background()

	out, err := e.handler.Handle(ctx, startEvent(t))
	if err != nil {
		t.Fatalf("Handle(start) error = %v", err)
	}
	want := &StartOutput{JobID: jobID, TextractJobID: e.rec.JobID, CallbackKey: s3.TextractCallbackKey(jobID)}
	if *out != *want {
		t.Errorf("start output = %+v, want %+v", out, want)
	}
	assertSmall(t, "start output", out)

	// Textract には完了通知の宛先と、通知から jobId を引くための JobTag が渡ること
	if len(e.textract.startInputs) != 1 {
		t.Fatalf("StartDocumentAnalysis calls = %d, want 1", len(e.textract.startInputs))
	}
	in := e.textract.startInputs[0]
	if aws.ToString(in.JobTag) != jobID {
		t.Errorf("JobTag = %q, want %q", aws.ToString(in.JobTag), jobID)
	}
	if in.NotificationChannel == nil || aws.ToString(in.NotificationChannel.SNSTopicArn) != topicARN || aws.ToString(in.NotificationChannel.RoleArn) != roleARN {
		t.Errorf("NotificationChannel = %+v", in.NotificationChannel)
	}
	if !reflect.DeepEqual(in.FeatureTypes, featureTypes) {
		t.Errorf("FeatureTypes = %v, want %v", in.FeatureTypes, featureTypes)
	}
	if aws.ToString(in.DocumentLocation.S3Object.Bucket) != testBucket || aws.ToString(in.DocumentLocation.S3Object.Name) != s3.OriginalPDFKey(jobID) {
		t.Errorf("DocumentLocation = %+v", in.DocumentLocation.S3Object)
	}

	// タスクトークンと Source が退避されていること
	var cb Callback
	e.object(t, s3.TextractCallbackKey(jobID), &cb)
	wantSource := domain.Source{
		Bucket:       testBucket,
		Key:          s3.OriginalPDFKey(jobID),
		Filename:     s3.OriginalPDFKey(jobID),
		SHA256:       jobID,
		Language:     domain.LanguageEnglish,
		PageCount:    2,
		HasTextLayer: true,
		UploadedAt:   uploadedAt,
	}
	if cb.JobID != jobID || cb.TaskToken != taskToken || cb.TextractJobID != e.rec.JobID || !cb.StartedAt.Equal(startedAt) {
		t.Errorf("callback = %+v", cb)
	}
	if !reflect.DeepEqual(cb.FeatureTypes, featureTypes) {
		t.Errorf("callback featureTypes = %v, want %v", cb.FeatureTypes, featureTypes)
	}
	if !reflect.DeepEqual(cb.Source, wantSource) {
		t.Errorf("callback source = %+v, want %+v", cb.Source, wantSource)
	}
	// 起動の時点では応答も結果も無いこと
	if len(e.states.successes)+len(e.states.failures) != 0 {
		t.Fatal("起動の時点で Step Functions に応答している")
	}
	e.assertAbsent(t, s3.TextractRawKey(jobID))
	e.assertAbsent(t, s3.TextractDocumentKey(jobID))

	// 完了通知
	out, err = e.handler.Handle(ctx, snsEvent(t, completion(jobID, e.rec.JobID, types.JobStatusSucceeded)))
	if err != nil {
		t.Fatalf("Handle(callback) error = %v", err)
	}
	if out != nil {
		t.Errorf("callback output = %+v, want nil", out)
	}

	var raw textract.AnalysisResult
	e.object(t, s3.TextractRawKey(jobID), &raw)
	if raw.JobID != e.rec.JobID || raw.JobStatus != types.JobStatusSucceeded {
		t.Errorf("raw = {JobID: %q, JobStatus: %q}", raw.JobID, raw.JobStatus)
	}
	var wantBlocks int
	for _, p := range e.rec.AnalysisPages {
		wantBlocks += len(p.Blocks)
	}
	if len(raw.Blocks) != wantBlocks {
		t.Errorf("raw blocks = %d, want %d (ページングを畳んだ全 Block)", len(raw.Blocks), wantBlocks)
	}

	// 正規化前の結果は work/ に置き、outputs/ の最終成果物は finalizer だけが書く
	var doc domain.Document
	e.object(t, s3.TextractDocumentKey(jobID), &doc)
	e.assertAbsent(t, s3.ResultTextractKey(jobID))
	if doc.JobID != jobID || doc.SchemaVersion != domain.SchemaVersion {
		t.Errorf("document = {JobID: %q, SchemaVersion: %q}", doc.JobID, doc.SchemaVersion)
	}
	if !reflect.DeepEqual(doc.Source, wantSource) {
		t.Errorf("document source = %+v, want %+v (退避した値を素通しすること)", doc.Source, wantSource)
	}
	if doc.Metadata.Title != "Learning to Structure Scientific Documents" {
		t.Errorf("title = %q (Bedrock の記録が再生されていない)", doc.Metadata.Title)
	}
	p := doc.Provenance
	if p.Route != domain.RouteTextract {
		t.Errorf("route = %q, want textract", p.Route)
	}
	if !p.ExtractedAt.Equal(finishedAt) {
		t.Errorf("extractedAt = %v, want %v", p.ExtractedAt, finishedAt)
	}
	if want := finishedAt.Sub(startedAt).Milliseconds(); p.DurationMs != want {
		t.Errorf("durationMs = %d, want %d (Textract の開始から結果の保存まで)", p.DurationMs, want)
	}
	if got := strings.Join(p.Cost.TextractFeatures, ","); got != "LAYOUT,TABLES" {
		t.Errorf("textractFeatures = %q", got)
	}
	if p.Cost.BedrockModel != recordedModelID || p.Cost.TextractPages == 0 {
		t.Errorf("cost = %+v", p.Cost)
	}

	// Step Functions には S3 キーだけを返すこと
	if len(e.states.failures) != 0 {
		t.Fatalf("SendTaskFailure が呼ばれている: %+v", e.states.failures[0])
	}
	if len(e.states.successes) != 1 {
		t.Fatalf("SendTaskSuccess calls = %d, want 1", len(e.states.successes))
	}
	success := e.states.successes[0]
	if aws.ToString(success.TaskToken) != taskToken {
		t.Errorf("task token = %q", aws.ToString(success.TaskToken))
	}
	wantOutput := `{"jobId":"` + jobID + `","resultKey":"` + s3.TextractDocumentKey(jobID) + `","rawKey":"` + s3.TextractRawKey(jobID) + `"}`
	if got := aws.ToString(success.Output); got != wantOutput {
		t.Errorf("task output = %s\nwant %s", got, wantOutput)
	}
	if len(aws.ToString(success.Output)) > 1024 {
		t.Errorf("task output = %d bytes, want 1KB 未満", len(aws.ToString(success.Output)))
	}
}

func TestHandleCallbackTextractFailed(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cb := e.seedCallback(t, jobID, e.rec.JobID)

	if _, err := e.handler.Handle(context.Background(), snsEvent(t, completion(jobID, e.rec.JobID, types.JobStatusFailed))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(e.states.successes) != 0 {
		t.Fatal("SendTaskSuccess が呼ばれている")
	}
	if len(e.states.failures) != 1 {
		t.Fatalf("SendTaskFailure calls = %d, want 1", len(e.states.failures))
	}
	failure := e.states.failures[0]
	if aws.ToString(failure.TaskToken) != cb.TaskToken {
		t.Errorf("task token = %q", aws.ToString(failure.TaskToken))
	}
	if aws.ToString(failure.Error) != FailureTextractJob {
		t.Errorf("error = %q, want %q", aws.ToString(failure.Error), FailureTextractJob)
	}
	var cause FailureCause
	if err := json.Unmarshal([]byte(aws.ToString(failure.Cause)), &cause); err != nil {
		t.Fatalf("cause %q is not json: %v", aws.ToString(failure.Cause), err)
	}
	if cause.JobID != jobID || cause.TextractJobID != e.rec.JobID || !strings.Contains(cause.Message, "FAILED") {
		t.Errorf("cause = %+v", cause)
	}

	// 失敗したジョブの結果は取りに行かない
	e.assertAbsent(t, s3.TextractRawKey(jobID))
	e.assertAbsent(t, s3.TextractDocumentKey(jobID))
}

// PARTIAL_SUCCESS でも Block は取得できるため、成功と同じく結果を保存して SendTaskSuccess を呼ぶ
func TestHandleCallbackTreatsPartialSuccessAsSuccess(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedCallback(t, jobID, e.rec.JobID)

	if _, err := e.handler.Handle(context.Background(), snsEvent(t, completion(jobID, e.rec.JobID, types.JobStatusPartialSuccess))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(e.states.failures) != 0 || len(e.states.successes) != 1 {
		t.Fatalf("successes = %d, failures = %d, want 1 and 0", len(e.states.successes), len(e.states.failures))
	}
	var doc domain.Document
	e.object(t, s3.TextractDocumentKey(jobID), &doc)
}

// Textract は成功したが後段が失敗した場合は ExtractFailed を返す
func TestHandleCallbackExtractFailed(t *testing.T) {
	t.Parallel()

	getErr := &types.InternalServerError{Message: aws.String("textract is unavailable")}

	tests := map[string]struct {
		id          string
		getErr      error
		wantMessage string
		wantRaw     bool
	}{
		// この jobId には Bedrock の記録が無いため再生が失敗する
		"正常系_Bedrock の構造化に失敗した場合_課金済みの生出力は残したうえで ExtractFailed を返すこと": {
			id:          "job-without-bedrock-recording",
			wantMessage: bedrock.ErrRecordingNotFound.Error(),
			wantRaw:     true,
		},
		"正常系_結果の取得に失敗した場合_生出力を保存せず ExtractFailed を返すこと": {
			id:          jobID,
			getErr:      getErr,
			wantMessage: getErr.Error(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedCallback(t, tt.id, e.rec.JobID)
			e.textract.getErr = tt.getErr

			if _, err := e.handler.Handle(context.Background(), snsEvent(t, completion(tt.id, e.rec.JobID, types.JobStatusSucceeded))); err != nil {
				t.Fatalf("Handle() error = %v", err)
			}

			if len(e.states.successes) != 0 {
				t.Fatal("SendTaskSuccess が呼ばれている")
			}
			if len(e.states.failures) != 1 {
				t.Fatalf("SendTaskFailure calls = %d, want 1", len(e.states.failures))
			}
			failure := e.states.failures[0]
			if aws.ToString(failure.Error) != FailureExtract {
				t.Errorf("error = %q, want %q", aws.ToString(failure.Error), FailureExtract)
			}
			var cause FailureCause
			if err := json.Unmarshal([]byte(aws.ToString(failure.Cause)), &cause); err != nil {
				t.Fatalf("cause %q is not json: %v", aws.ToString(failure.Cause), err)
			}
			if cause.JobID != tt.id || cause.TextractJobID != e.rec.JobID || !strings.Contains(cause.Message, tt.wantMessage) {
				t.Errorf("cause = %+v", cause)
			}

			if tt.wantRaw {
				var raw textract.AnalysisResult
				e.object(t, s3.TextractRawKey(tt.id), &raw)
			} else {
				e.assertAbsent(t, s3.TextractRawKey(tt.id))
			}
			e.assertAbsent(t, s3.TextractDocumentKey(tt.id))
		})
	}
}

// Retry で再起動された後に届く古いジョブの通知は無視する
func TestHandleCallbackIgnoresStaleJob(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedCallback(t, jobID, "newer-textract-job")

	if _, err := e.handler.Handle(context.Background(), snsEvent(t, completion(jobID, e.rec.JobID, types.JobStatusSucceeded))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(e.states.successes)+len(e.states.failures) != 0 {
		t.Error("古いジョブの通知で Step Functions に応答している")
	}
	e.assertAbsent(t, s3.TextractRawKey(jobID))
	e.assertAbsent(t, s3.TextractDocumentKey(jobID))
}

// 退避が無い通知はエラーにして Lambda の非同期呼び出しの再試行に委ねる
func TestHandleCallbackWithoutCallbackObject(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	_, err := e.handler.Handle(context.Background(), snsEvent(t, completion(jobID, e.rec.JobID, types.JobStatusSucceeded)))
	if !errors.Is(err, s3.ErrNotFound) {
		t.Fatalf("Handle() error = %v, want s3.ErrNotFound", err)
	}
	if len(e.states.successes)+len(e.states.failures) != 0 {
		t.Error("退避が無いのに Step Functions に応答している")
	}
}

func TestHandleCallbackRejectsNotificationWithoutJobTag(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	n := completion(jobID, e.rec.JobID, types.JobStatusSucceeded)
	n.JobTag = ""

	if _, err := e.handler.Handle(context.Background(), snsEvent(t, n)); !errors.Is(err, ErrEmptyJobTag) {
		t.Fatalf("Handle() error = %v, want ErrEmptyJobTag", err)
	}
}

// Task がもう待っていない場合は再配送しても届かないため、応答を捨てて正常終了にする
func TestHandleCallbackWhenTaskIsGone(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status types.JobStatus
	}{
		"正常系_成功の応答が届かない場合_エラーにせず結果は残すこと": {status: types.JobStatusSucceeded},
		"正常系_失敗の応答が届かない場合_エラーにしないこと":     {status: types.JobStatusFailed},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedCallback(t, jobID, e.rec.JobID)
			e.states.err = &sfntypes.TaskTimedOut{}

			if _, err := e.handler.Handle(context.Background(), snsEvent(t, completion(jobID, e.rec.JobID, tt.status))); err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if tt.status == types.JobStatusSucceeded {
				var doc domain.Document
				e.object(t, s3.TextractDocumentKey(jobID), &doc)
			}
		})
	}
}

// 応答の失敗が一時的なものならエラーを返し、Lambda の非同期呼び出しの再試行で再度応答を試みる
func TestHandleCallbackReturnsResponseError(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedCallback(t, jobID, e.rec.JobID)
	respondErr := errors.New("sfn unavailable")
	e.states.err = respondErr

	if _, err := e.handler.Handle(context.Background(), snsEvent(t, completion(jobID, e.rec.JobID, types.JobStatusFailed))); !errors.Is(err, respondErr) {
		t.Fatalf("Handle() error = %v, want %v", err, respondErr)
	}
}

// Textract の一時的な失敗だけを RetryableError で返し、Step Functions の Retry が型名で拾えることを確かめる
func TestHandleStartClassifiesTextractErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err           error
		wantRetryable bool
	}{
		"正常系_ProvisionedThroughputExceededException の場合_RetryableError になること": {err: &types.ProvisionedThroughputExceededException{}, wantRetryable: true},
		"正常系_LimitExceededException の場合_RetryableError になること":                 {err: &types.LimitExceededException{}, wantRetryable: true},
		"正常系_ThrottlingException の場合_RetryableError になること":                    {err: &types.ThrottlingException{}, wantRetryable: true},
		"正常系_InternalServerError の場合_RetryableError になること":                    {err: &types.InternalServerError{}, wantRetryable: true},
		"正常系_InvalidS3ObjectException の場合_PermanentError になること":               {err: &types.InvalidS3ObjectException{}, wantRetryable: false},
		"正常系_UnsupportedDocumentException の場合_PermanentError になること":           {err: &types.UnsupportedDocumentException{}, wantRetryable: false},
		"正常系_AccessDeniedException の場合_PermanentError になること":                  {err: &types.AccessDeniedException{}, wantRetryable: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedPDF(t)
			e.textract.startErr = tt.err

			_, err := e.handler.Handle(context.Background(), startEvent(t))
			if err == nil {
				t.Fatal("Handle() error = nil, want an error")
			}
			if !errors.Is(err, tt.err) {
				t.Errorf("error %v does not wrap %v", err, tt.err)
			}

			// aws-lambda-go は errorType にポインタの Elem().Name() を用いる (lambda/errors.go の lambdaErrorResponse)
			wantType := "PermanentError"
			if tt.wantRetryable {
				wantType = "RetryableError"
			}
			if got := reflect.TypeOf(err).Elem().Name(); got != wantType {
				t.Errorf("errorType = %q, want %q", got, wantType)
			}
			var retryable *RetryableError
			if got := errors.As(err, &retryable); got != tt.wantRetryable {
				t.Errorf("errors.As(RetryableError) = %v, want %v", got, tt.wantRetryable)
			}

			// 開始できていないので退避もしない
			e.assertAbsent(t, s3.TextractCallbackKey(jobID))
		})
	}
}

func TestHandleStartRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw     string
		seedPDF bool
		want    error
	}{
		"異常系_jobId が無い場合_ErrEmptyJobID を PermanentError で返すこと":         {raw: `{"taskToken":"token"}`, seedPDF: true, want: ErrEmptyJobID},
		"異常系_taskToken が無い場合_ErrEmptyTaskToken を PermanentError で返すこと": {raw: `{"jobId":"` + jobID + `"}`, seedPDF: true, want: ErrEmptyTaskToken},
		"異常系_PDF が無い場合_s3.ErrNotFound を PermanentError で返すこと":          {raw: `{"jobId":"` + jobID + `","taskToken":"token"}`, want: s3.ErrNotFound},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if tt.seedPDF {
				e.seedPDF(t)
			}

			_, err := e.handler.Handle(context.Background(), json.RawMessage(tt.raw))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Handle() error = %v, want %v", err, tt.want)
			}
			var permanent *PermanentError
			if !errors.As(err, &permanent) {
				t.Errorf("Handle() error = %T, want *PermanentError", err)
			}
			if len(e.textract.startInputs) != 0 {
				t.Error("入力不正なのに Textract を開始している")
			}
		})
	}

	// JSON として読めない入力も PermanentError にする
	var permanent *PermanentError
	if _, err := newEnv(t).handler.Handle(context.Background(), json.RawMessage(`not json`)); !errors.As(err, &permanent) {
		t.Errorf("Handle() error = %T (%v), want *PermanentError", err, err)
	}
}

// 退避に失敗した場合はエラーを返して Step Functions の Retry に委ね、開始済みのジョブは孤児として捨てる
func TestHandleStartFailsWhenCallbackCannotBeSaved(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.seedPDF(t)
	putErr := errors.New("s3 unavailable")
	e.docs.PutErr = putErr

	_, err := e.handler.Handle(context.Background(), startEvent(t))
	if !errors.Is(err, putErr) {
		t.Fatalf("Handle() error = %v, want %v", err, putErr)
	}
	var permanent *PermanentError
	if !errors.As(err, &permanent) {
		t.Errorf("Handle() error = %T, want *PermanentError", err)
	}
	// Textract は開始済みであり、Retry では別のジョブが立ち上がる
	if len(e.textract.startInputs) != 1 {
		t.Errorf("StartDocumentAnalysis calls = %d, want 1", len(e.textract.startInputs))
	}
	e.assertAbsent(t, s3.TextractCallbackKey(jobID))
}

// SNS の配送だけを完了通知として扱い、それ以外の Records を持つ入力を通知と取り違えない
func TestAsSNSEvent(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw  string
		want bool
	}{
		"正常系_EventSource が aws:sns の場合_SNS の配送と判定すること": {raw: `{"Records":[{"EventSource":"aws:sns","Sns":{"Message":"{}"}}]}`, want: true},
		"正常系_起動の入力の場合_SNS の配送と判定しないこと":                 {raw: `{"jobId":"job-1","taskToken":"token"}`, want: false},
		"正常系_Records が空の場合_SNS の配送と判定しないこと":            {raw: `{"Records":[]}`, want: false},
		"正常系_EventSource が aws:s3 の場合_SNS の配送と判定しないこと": {raw: `{"Records":[{"eventSource":"aws:s3","s3":{}}]}`, want: false},
		"正常系_SNS 以外の Records が混在する場合_SNS の配送と判定しないこと":  {raw: `{"Records":[{"EventSource":"aws:sns"},{"EventSource":"aws:s3"}]}`, want: false},
		"異常系_JSON として読めない場合_SNS の配送と判定しないこと":           {raw: `not json`, want: false},
		"異常系_Records がオブジェクトの配列でない場合_SNS の配送と判定しないこと":  {raw: `{"Records":"aws:sns"}`, want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ev, got := asSNSEvent(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Fatalf("asSNSEvent() = %v, want %v", got, tt.want)
			}
			if got && len(ev.Records) == 0 {
				t.Error("asSNSEvent() returned an event without records")
			}
		})
	}
}

func TestParseFeatureTypes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		spec    string
		want    []types.FeatureType
		wantErr error
	}{
		"正常系_カンマ区切りの場合_順に変換されること":             {spec: "LAYOUT,TABLES", want: []types.FeatureType{types.FeatureTypeLayout, types.FeatureTypeTables}},
		"正常系_空白と末尾のカンマを含む場合_無視されること":          {spec: " LAYOUT , TABLES ,", want: []types.FeatureType{types.FeatureTypeLayout, types.FeatureTypeTables}},
		"正常系_1 件の場合_その 1 件になること":              {spec: "FORMS", want: []types.FeatureType{types.FeatureTypeForms}},
		"異常系_空文字の場合_ErrInvalidInput になること":    {spec: "", wantErr: textract.ErrInvalidInput},
		"異常系_未知の値を含む場合_ErrInvalidInput になること": {spec: "LAYOUT,NOPE", wantErr: textract.ErrInvalidInput},
		"異常系_重複がある場合_ErrInvalidInput になること":   {spec: "LAYOUT,LAYOUT", wantErr: textract.ErrInvalidInput},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFeatureTypes(tt.spec)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFeatureTypes(%q) error = %v", tt.spec, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseFeatureTypes(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}
