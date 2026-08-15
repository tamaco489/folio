package s3_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
)

func TestKeys(t *testing.T) {
	t.Parallel()

	const jobID = "01JQ0000000000000000000000"

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"original pdf", s3.OriginalPDFKey(jobID), "uploads/" + jobID + "/original.pdf"},
		{"page image", s3.PageImageKey(jobID, 1), "work/" + jobID + "/pages/page-0001.png"},
		{"page image max", s3.PageImageKey(jobID, s3.MaxPageNumber), "work/" + jobID + "/pages/page-9999.png"},
		{"textract raw", s3.TextractRawKey(jobID), "work/" + jobID + "/textract/raw.json"},
		{"text layer", s3.TextLayerKey(jobID), "work/" + jobID + "/text/layer.txt"},
		{"result textract", s3.ResultTextractKey(jobID), "outputs/" + jobID + "/result-textract.json"},
		{"result bedrock", s3.ResultBedrockKey(jobID), "outputs/" + jobID + "/result-bedrock.json"},
		{"comparison", s3.ComparisonKey(jobID), "outputs/" + jobID + "/comparison.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// ページ画像は辞書順とページ順が一致していないと後段で並べ替えられない
func TestPageImageKeyLexicalOrder(t *testing.T) {
	t.Parallel()

	pages := []int{1, 2, 9, 10, 99, 100, 999, 1000, 3000, s3.MaxPageNumber}

	keys := make([]string, 0, len(pages))
	for _, page := range pages {
		keys = append(keys, s3.PageImageKey("job", page))
	}

	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	for i := range keys {
		if keys[i] != sorted[i] {
			t.Fatalf("lexical order differs from page order: %v", sorted)
		}
	}
}

func TestKeyPrefixesAreDistinct(t *testing.T) {
	t.Parallel()

	// uploads 配下だけに S3 イベント通知を設定するため、派生物が uploads に入ってはならない
	derived := []string{
		s3.PageImageKey("job", 1),
		s3.TextractRawKey("job"),
		s3.TextLayerKey("job"),
		s3.ResultTextractKey("job"),
		s3.ResultBedrockKey("job"),
		s3.ComparisonKey("job"),
	}

	for _, key := range derived {
		if strings.HasPrefix(key, s3.PrefixUploads+"/") {
			t.Errorf("derived key %q is under the %q prefix", key, s3.PrefixUploads)
		}
	}
}

func TestJobIDFromUploadKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		want    string
		wantErr bool
	}{
		{name: "upload key", key: "uploads/job-1/original.pdf", want: "job-1"},
		{name: "work key", key: "work/job-1/text/layer.txt", wantErr: true},
		{name: "output key", key: "outputs/job-1/comparison.json", wantErr: true},
		{name: "other object", key: "uploads/job-1/other.pdf", wantErr: true},
		{name: "empty job id", key: "uploads//original.pdf", wantErr: true},
		{name: "extra segment", key: "uploads/job-1/nested/original.pdf", wantErr: true},
		{name: "empty key", key: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := s3.JobIDFromUploadKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// 往復させることでイベント通知のキーと組み立て規則がずれていないことを確かめる
func TestJobIDFromUploadKeyRoundTrip(t *testing.T) {
	t.Parallel()

	const jobID = "01JQ0000000000000000000000"

	got, err := s3.JobIDFromUploadKey(s3.OriginalPDFKey(jobID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != jobID {
		t.Errorf("got %q, want %q", got, jobID)
	}
}
