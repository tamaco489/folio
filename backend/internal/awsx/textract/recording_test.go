package textract_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/textract"
)

// testdataDir は justfile に記した配置規約に対応する
const testdataDir = "../../../testdata/textract"

const samplePaperID = "1706.03762"

func loadSample(t *testing.T) *textract.Recording {
	t.Helper()
	rec, err := textract.LoadRecording(textract.RecordingPath(testdataDir, samplePaperID))
	if err != nil {
		t.Fatalf("LoadRecording: %v", err)
	}
	return rec
}

func TestReplayGetDocumentAnalysisMergesPages(t *testing.T) {
	t.Parallel()

	rec := loadSample(t)
	replayer, err := textract.NewReplayer(rec)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}

	res, err := textract.New(replayer).GetDocumentAnalysis(context.Background(), rec.JobID)
	if err != nil {
		t.Fatalf("GetDocumentAnalysis: %v", err)
	}

	if res.Pages != len(rec.AnalysisPages) {
		t.Fatalf("pages = %d, want %d", res.Pages, len(rec.AnalysisPages))
	}
	want := 0
	for _, p := range rec.AnalysisPages {
		want += len(p.Blocks)
	}
	if len(res.Blocks) != want {
		t.Fatalf("blocks = %d, want %d", len(res.Blocks), want)
	}
	if res.JobStatus != types.JobStatusSucceeded {
		t.Fatalf("job status = %q", res.JobStatus)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(res.Warnings))
	}
	if res.DocumentMetadata == nil || aws.ToInt32(res.DocumentMetadata.Pages) != 2 {
		t.Fatalf("document metadata = %+v", res.DocumentMetadata)
	}
}

func TestReplayPreservesBlockDetails(t *testing.T) {
	t.Parallel()

	rec := loadSample(t)
	replayer, err := textract.NewReplayer(rec)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	res, err := textract.New(replayer).GetDocumentAnalysis(context.Background(), rec.JobID)
	if err != nil {
		t.Fatalf("GetDocumentAnalysis: %v", err)
	}

	var title, cell *types.Block
	for i := range res.Blocks {
		switch res.Blocks[i].BlockType {
		case types.BlockTypeLine:
			if title == nil {
				title = &res.Blocks[i]
			}
		case types.BlockTypeCell:
			if cell == nil {
				cell = &res.Blocks[i]
			}
		}
	}

	if title == nil || aws.ToString(title.Text) != "Attention Is All You Need" {
		t.Fatalf("line block = %+v", title)
	}
	if title.Geometry == nil || title.Geometry.BoundingBox == nil || title.Geometry.BoundingBox.Width == 0 {
		t.Fatal("geometry was lost in the replay")
	}
	if aws.ToFloat32(title.Confidence) == 0 {
		t.Fatal("confidence was lost in the replay")
	}
	if cell == nil || aws.ToInt32(cell.RowIndex) != 1 || aws.ToInt32(cell.ColumnIndex) != 1 {
		t.Fatalf("cell block = %+v", cell)
	}
	if len(cell.EntityTypes) != 1 || cell.EntityTypes[0] != types.EntityTypeColumnHeader {
		t.Fatalf("entity types = %v", cell.EntityTypes)
	}
}

func TestReplayDetectDocumentText(t *testing.T) {
	t.Parallel()

	replayer, err := textract.NewReplayer(loadSample(t))
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}

	res, err := textract.New(replayer).DetectDocumentText(context.Background(), textract.DetectInput{
		Document: textract.S3Location{Bucket: "folio-dev", Key: "work/job-1/pages/page-001.png"},
	})
	if err != nil {
		t.Fatalf("DetectDocumentText: %v", err)
	}
	if len(res.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(res.Blocks))
	}
}

func TestReplayStartDocumentAnalysisReturnsRecordedJobID(t *testing.T) {
	t.Parallel()

	rec := loadSample(t)
	replayer, err := textract.NewReplayer(rec)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}

	jobID, err := textract.New(replayer).StartDocumentAnalysis(context.Background(), textract.StartAnalysisInput{
		Document:     textract.S3Location{Bucket: "folio-dev", Key: "uploads/job-1/original.pdf"},
		FeatureTypes: rec.FeatureTypes,
	})
	if err != nil {
		t.Fatalf("StartDocumentAnalysis: %v", err)
	}
	if jobID != rec.JobID {
		t.Fatalf("job id = %q, want %q", jobID, rec.JobID)
	}
}

func TestReplayRejectsUnknownJobID(t *testing.T) {
	t.Parallel()

	replayer, err := textract.NewReplayer(loadSample(t))
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	if _, err := textract.New(replayer).GetDocumentAnalysis(context.Background(), "other-job"); err == nil {
		t.Fatal("err = nil, want an error for the unknown job id")
	}
}

func TestRecordingValidateRejectsBrokenPaging(t *testing.T) {
	t.Parallel()

	tests := map[string]textract.Recording{
		"異常系_最終ページにトークンが残る場合_検証エラーになること": {
			AnalysisPages: []textract.AnalysisPage{
				{JobStatus: types.JobStatusSucceeded, NextToken: "dangling"},
			},
		},
		"異常系_中間ページにトークンがない場合_検証エラーになること": {
			AnalysisPages: []textract.AnalysisPage{
				{JobStatus: types.JobStatusSucceeded},
				{JobStatus: types.JobStatusSucceeded},
			},
		},
		"異常系_ジョブ状態がない場合_検証エラーになること": {
			AnalysisPages: []textract.AnalysisPage{{}},
		},
		"境界値_記録が空の場合_検証エラーになること": {},
	}

	for name, rec := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := rec.Validate(); err == nil {
				t.Fatal("err = nil, want a validation error")
			}
		})
	}
}

func TestRecorderRoundTrip(t *testing.T) {
	t.Parallel()

	pages := []*awstextract.GetDocumentAnalysisOutput{
		{
			JobStatus:                   types.JobStatusSucceeded,
			AnalyzeDocumentModelVersion: aws.String("1.0"),
			DocumentMetadata:            &types.DocumentMetadata{Pages: aws.Int32(1)},
			Blocks:                      []types.Block{{BlockType: types.BlockTypeLine, Text: aws.String("first"), Confidence: aws.Float32(97.5)}},
			NextToken:                   aws.String("token-2"),
		},
		{
			JobStatus:                   types.JobStatusSucceeded,
			AnalyzeDocumentModelVersion: aws.String("1.0"),
			DocumentMetadata:            &types.DocumentMetadata{Pages: aws.Int32(1)},
			Blocks:                      []types.Block{{BlockType: types.BlockTypeLine, Text: aws.String("second")}},
		},
	}
	calls := 0
	inner := &fakeAPI{
		start: func(*awstextract.StartDocumentAnalysisInput) (*awstextract.StartDocumentAnalysisOutput, error) {
			return &awstextract.StartDocumentAnalysisOutput{JobId: aws.String("recorded-job")}, nil
		},
		get: func(*awstextract.GetDocumentAnalysisInput) (*awstextract.GetDocumentAnalysisOutput, error) {
			out := pages[calls]
			calls++
			return out, nil
		},
		detect: func(*awstextract.DetectDocumentTextInput) (*awstextract.DetectDocumentTextOutput, error) {
			return &awstextract.DetectDocumentTextOutput{
				Blocks:                         []types.Block{{BlockType: types.BlockTypeLine, Text: aws.String("baseline")}},
				DetectDocumentTextModelVersion: aws.String("1.0"),
			}, nil
		},
	}

	recorder := textract.NewRecorder(inner, "9999.00001")
	recording := textract.New(recorder)

	jobID, err := recording.StartDocumentAnalysis(context.Background(), textract.StartAnalysisInput{
		Document:     textract.S3Location{Bucket: "folio-dev", Key: "uploads/job-1/original.pdf"},
		FeatureTypes: []types.FeatureType{types.FeatureTypeLayout, types.FeatureTypeTables},
	})
	if err != nil {
		t.Fatalf("StartDocumentAnalysis: %v", err)
	}
	live, err := recording.GetDocumentAnalysis(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetDocumentAnalysis: %v", err)
	}
	if _, err := recording.DetectDocumentText(context.Background(), textract.DetectInput{Bytes: []byte("png")}); err != nil {
		t.Fatalf("DetectDocumentText: %v", err)
	}

	dir := t.TempDir()
	if err := recorder.WriteFile(dir); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := textract.LoadRecording(filepath.Join(dir, "9999.00001.json"))
	if err != nil {
		t.Fatalf("LoadRecording: %v", err)
	}
	if loaded.JobID != "recorded-job" || len(loaded.FeatureTypes) != 2 {
		t.Fatalf("recording = %+v", loaded)
	}
	if loaded.Detect == nil || len(loaded.Detect.Blocks) != 1 {
		t.Fatalf("detect = %+v", loaded.Detect)
	}

	replayer, err := textract.NewReplayer(loaded)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	replayed, err := textract.New(replayer).GetDocumentAnalysis(context.Background(), loaded.JobID)
	if err != nil {
		t.Fatalf("replayed GetDocumentAnalysis: %v", err)
	}

	if replayed.Pages != live.Pages || len(replayed.Blocks) != len(live.Blocks) {
		t.Fatalf("replayed = %d pages %d blocks, live = %d pages %d blocks",
			replayed.Pages, len(replayed.Blocks), live.Pages, len(live.Blocks))
	}
	for i := range live.Blocks {
		if aws.ToString(replayed.Blocks[i].Text) != aws.ToString(live.Blocks[i].Text) {
			t.Fatalf("block %d text = %q, want %q", i, aws.ToString(replayed.Blocks[i].Text), aws.ToString(live.Blocks[i].Text))
		}
		if aws.ToFloat32(replayed.Blocks[i].Confidence) != aws.ToFloat32(live.Blocks[i].Confidence) {
			t.Fatalf("block %d confidence differs", i)
		}
	}
	if replayed.ModelVersion != live.ModelVersion {
		t.Fatalf("model version = %q, want %q", replayed.ModelVersion, live.ModelVersion)
	}
}

func TestRecorderSkipsInProgressResponse(t *testing.T) {
	t.Parallel()

	inner := &fakeAPI{
		get: func(*awstextract.GetDocumentAnalysisInput) (*awstextract.GetDocumentAnalysisOutput, error) {
			return &awstextract.GetDocumentAnalysisOutput{JobStatus: types.JobStatusInProgress}, nil
		},
	}
	recorder := textract.NewRecorder(inner, "9999.00002")

	if _, err := textract.New(recorder).GetDocumentAnalysis(context.Background(), "job-1"); !errors.Is(err, textract.ErrJobInProgress) {
		t.Fatalf("err = %v, want ErrJobInProgress", err)
	}
	if len(recorder.Recording().AnalysisPages) != 0 {
		t.Fatal("an in-progress response was recorded")
	}
}

func TestParseCompletionNotification(t *testing.T) {
	t.Parallel()

	message := []byte(`{
		"JobId": "0b1c2d3e",
		"Status": "SUCCEEDED",
		"API": "StartDocumentAnalysis",
		"JobTag": "1706.03762",
		"Timestamp": 1755216000000,
		"DocumentLocation": {"S3ObjectName": "uploads/job-1/original.pdf", "S3Bucket": "folio-dev"}
	}`)

	n, err := textract.ParseCompletionNotification(message)
	if err != nil {
		t.Fatalf("ParseCompletionNotification: %v", err)
	}
	if n.JobID != "0b1c2d3e" || !n.Succeeded() {
		t.Fatalf("notification = %+v", n)
	}
	if n.DocumentLocation.Bucket != "folio-dev" || n.DocumentLocation.Name != "uploads/job-1/original.pdf" {
		t.Fatalf("document location = %+v", n.DocumentLocation)
	}
	if n.Time().IsZero() {
		t.Fatal("timestamp was not converted")
	}

	if _, err := textract.ParseCompletionNotification([]byte(`{"Status":"SUCCEEDED"}`)); !errors.Is(err, textract.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := textract.ParseCompletionNotification([]byte(`not json`)); err == nil {
		t.Fatal("err = nil, want a parse error")
	}
}

func TestRecordingPath(t *testing.T) {
	t.Parallel()

	if got, want := textract.RecordingPath("testdata/textract", samplePaperID), filepath.Join("testdata/textract", samplePaperID+".json"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
