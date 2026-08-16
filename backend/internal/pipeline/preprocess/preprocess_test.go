package preprocess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/s3/s3test"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

const testBucket = "test-folio-documents"

// pngMagic は PNG ファイルの先頭 8 バイト
var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// requirePoppler は poppler が使えない環境でテストをスキップする (CI に poppler が入っていない可能性があるため)
func requirePoppler(t *testing.T) *pdf.Runner {
	t.Helper()

	for _, name := range []string{"pdfinfo", "pdftoppm", "pdftotext"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("poppler が見つからないためスキップする: %s", name)
		}
	}
	// キーとアップロードの検証に画質は要らないため解像度を下げて実行時間を抑える
	return pdf.NewRunner(pdf.WithBinDir(""), pdf.WithDPI(50), pdf.WithTimeout(time.Minute))
}

// newTestHandler はフェイクの S3 と一時ディレクトリでハンドラを組み立てる
func newTestHandler(t *testing.T, runner *pdf.Runner, opts ...Option) (*Handler, *s3test.Fake, string) {
	t.Helper()

	fake := s3test.NewFake()
	baseDir := t.TempDir()
	opts = append([]Option{WithBaseDir(baseDir)}, opts...)
	return New(s3.New(fake, testBucket), runner, opts...), fake, baseDir
}

// assertWorkDirCleaned は作業ディレクトリが残っていないことを確かめる (Lambda の /tmp は次の実行に引き継がれる)
func assertWorkDirCleaned(t *testing.T, baseDir string) {
	t.Helper()

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("read base dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("作業ディレクトリが残っている: %d 件", len(entries))
	}
}

func TestHandle(t *testing.T) {
	runner := requirePoppler(t)
	// チャンクを跨がせて、アップロード済みのページを消し損ねていないかを確かめる
	// 消し残すと次のチャンクの Rasterize が前のチャンクのファイルまで拾い ErrPageCountMismatch になる
	handler, fake, baseDir := newTestHandler(t, runner, WithChunkPages(2))

	const jobID = "job-with-text-layer"
	lines := []string{
		"Structured extraction of scholarly documents",
		"Abstract This paper evaluates two extraction routes",
		"and compares their fidelity against the source text.",
	}
	page := textPage(lines)
	fake.Seed(testBucket, s3.OriginalPDFKey(jobID), s3test.Object{
		Body: buildTestPDF([]string{page, page, page}),
	})

	got, err := handler.Handle(context.Background(), Input{JobID: jobID})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	want := Output{
		JobID:            jobID,
		PageCount:        3,
		HasTextLayer:     true,
		Language:         domain.LanguageEnglish,
		LanguageDetected: true,
		SkipTextract:     false,
		PageImagePrefix:  "work/" + jobID + "/pages/",
		TextLayerKey:     s3.TextLayerKey(jobID),
	}
	if got != want {
		t.Errorf("Handle() = %+v, want %+v", got, want)
	}

	for page := 1; page <= want.PageCount; page++ {
		key := s3.PageImageKey(jobID, page)
		obj, ok := fake.Object(testBucket, key)
		if !ok {
			t.Fatalf("ページ画像が保存されていない: %s", key)
		}
		if !strings.HasPrefix(string(obj.Body), string(pngMagic)) {
			t.Errorf("%s が PNG ではない", key)
		}
		if obj.ContentType != contentTypePNG {
			t.Errorf("%s の ContentType = %q, want %q", key, obj.ContentType, contentTypePNG)
		}
	}
	if _, ok := fake.Object(testBucket, s3.PageImageKey(jobID, want.PageCount+1)); ok {
		t.Errorf("存在しないページの画像が保存されている: %s", s3.PageImageKey(jobID, want.PageCount+1))
	}

	textObj, ok := fake.Object(testBucket, s3.TextLayerKey(jobID))
	if !ok {
		t.Fatalf("テキストレイヤーが保存されていない: %s", s3.TextLayerKey(jobID))
	}
	if !strings.Contains(string(textObj.Body), "Abstract") {
		t.Errorf("テキストレイヤーに本文が含まれない: %q", string(textObj.Body))
	}
	if textObj.ContentType != contentTypeText {
		t.Errorf("テキストレイヤーの ContentType = %q, want %q", textObj.ContentType, contentTypeText)
	}

	assertWorkDirCleaned(t, baseDir)

	// 出力にページ画像のキーが並んでいないこと (Step Functions の State 間は 256KB 上限)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if strings.Contains(string(encoded), pdf.PageFileName(1)) {
		t.Errorf("出力にページ画像のキーが含まれている: %s", encoded)
	}
}

func TestHandleWithoutTextLayer(t *testing.T) {
	runner := requirePoppler(t)
	handler, fake, baseDir := newTestHandler(t, runner)

	const jobID = "job-scanned"
	fake.Seed(testBucket, s3.OriginalPDFKey(jobID), s3test.Object{
		Body: buildTestPDF([]string{blankPage(), blankPage()}),
	})

	got, err := handler.Handle(context.Background(), Input{JobID: jobID})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	want := Output{
		JobID:            jobID,
		PageCount:        2,
		HasTextLayer:     false,
		Language:         domain.LanguageEnglish,
		LanguageDetected: false,
		SkipTextract:     false,
		PageImagePrefix:  "work/" + jobID + "/pages/",
		TextLayerKey:     s3.TextLayerKey(jobID),
	}
	if got != want {
		t.Errorf("Handle() = %+v, want %+v", got, want)
	}

	// テキストレイヤーが無くても経路 B はページ画像を要するため、ラスタライズは行う
	for page := 1; page <= want.PageCount; page++ {
		if _, ok := fake.Object(testBucket, s3.PageImageKey(jobID, page)); !ok {
			t.Errorf("ページ画像が保存されていない: %s", s3.PageImageKey(jobID, page))
		}
	}

	assertWorkDirCleaned(t, baseDir)
}

func TestHandleEmptyJobID(t *testing.T) {
	t.Parallel()

	handler, _, _ := newTestHandler(t, pdf.NewRunner())
	if _, err := handler.Handle(context.Background(), Input{}); !errors.Is(err, ErrEmptyJobID) {
		t.Errorf("Handle() error = %v, want ErrEmptyJobID", err)
	}
}

func TestHandleMissingPDF(t *testing.T) {
	t.Parallel()

	// PDF を取得できない時点で失敗するため poppler には到達しない
	handler, _, baseDir := newTestHandler(t, pdf.NewRunner())

	if _, err := handler.Handle(context.Background(), Input{JobID: "job-missing"}); !errors.Is(err, s3.ErrNotFound) {
		t.Errorf("Handle() error = %v, want s3.ErrNotFound", err)
	}
	assertWorkDirCleaned(t, baseDir)
}

func TestOutputHasNoCollectionFields(t *testing.T) {
	t.Parallel()

	// ページ画像のキーを列挙すると 200 ページで出力が肥大するため、件数に比例して伸びるフィールドを持たせない
	typ := reflect.TypeFor[Output]()
	for field := range typ.Fields() {
		switch field.Type.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			t.Errorf("Output.%s は %s であり件数に比例して伸びる", field.Name, field.Type.Kind())
		}
	}
}

func TestPageImagePrefix(t *testing.T) {
	t.Parallel()

	const jobID = "job-0001"
	got := PageImagePrefix(jobID)
	if want := "work/" + jobID + "/pages/"; got != want {
		t.Errorf("PageImagePrefix(%q) = %q, want %q", jobID, got, want)
	}
	// 後段がプレフィックスとページ番号からキーを導出できること
	if key := got + pdf.PageFileName(7); key != s3.PageImageKey(jobID, 7) {
		t.Errorf("導出したキー = %q, want %q", key, s3.PageImageKey(jobID, 7))
	}
}
