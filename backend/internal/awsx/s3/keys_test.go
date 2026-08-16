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

	tests := map[string]struct {
		got  string
		want string
	}{
		"正常系_受領した PDF の場合_uploads 配下のキーになること":           {s3.OriginalPDFKey(jobID), "uploads/" + jobID + "/original.pdf"},
		"正常系_ページ画像の場合_work 配下の 4 桁ゼロ埋めキーになること":          {s3.PageImageKey(jobID, 1), "work/" + jobID + "/pages/page-0001.png"},
		"正常系_Textract の生出力の場合_work 配下のキーになること":          {s3.TextractRawKey(jobID), "work/" + jobID + "/textract/raw.json"},
		"正常系_Textract のコールバック情報の場合_生出力と同じ階層のキーになること":    {s3.TextractCallbackKey(jobID), "work/" + jobID + "/textract/callback.json"},
		"正常系_経路 B のページ単位の結果の場合_work 配下の 4 桁ゼロ埋めキーになること": {s3.BedrockPageResultKey(jobID, 1), "work/" + jobID + "/bedrock/page-0001.json"},
		"正常系_テキストレイヤーの場合_work 配下のキーになること":               {s3.TextLayerKey(jobID), "work/" + jobID + "/text/layer.txt"},
		"正常系_経路 A の抽出結果の場合_outputs 配下のキーになること":          {s3.ResultTextractKey(jobID), "outputs/" + jobID + "/result-textract.json"},
		"正常系_経路 B の抽出結果の場合_outputs 配下のキーになること":          {s3.ResultBedrockKey(jobID), "outputs/" + jobID + "/result-bedrock.json"},
		"正常系_比較結果の場合_outputs 配下のキーになること":                {s3.ComparisonKey(jobID), "outputs/" + jobID + "/comparison.json"},
		"境界値_上限ページの画像の場合_4 桁に収まること":                     {s3.PageImageKey(jobID, s3.MaxPageNumber), "work/" + jobID + "/pages/page-9999.png"},
		"境界値_上限ページの結果の場合_4 桁に収まること":                     {s3.BedrockPageResultKey(jobID, s3.MaxPageNumber), "work/" + jobID + "/bedrock/page-9999.json"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// ページ単位のキーは辞書順とページ順が一致していないと後段で並べ替えられない
func TestPageKeyLexicalOrder(t *testing.T) {
	t.Parallel()

	pages := []int{1, 2, 9, 10, 99, 100, 999, 1000, 3000, s3.MaxPageNumber}

	tests := map[string]func(jobID string, page int) string{
		"正常系_ページ画像の場合_辞書順がページ順と一致すること":          s3.PageImageKey,
		"正常系_経路 B のページ単位の結果の場合_辞書順がページ順と一致すること": s3.BedrockPageResultKey,
	}

	for name, keyFn := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			keys := make([]string, 0, len(pages))
			for _, page := range pages {
				keys = append(keys, keyFn("job", page))
			}

			sorted := append([]string(nil), keys...)
			sort.Strings(sorted)

			for i := range keys {
				if keys[i] != sorted[i] {
					t.Fatalf("lexical order differs from page order: %v", sorted)
				}
			}
		})
	}
}

func TestKeyPrefixesAreDistinct(t *testing.T) {
	t.Parallel()

	// uploads 配下だけに S3 イベント通知を設定するため、派生物が uploads に入ってはならない
	derived := []string{
		s3.PageImageKey("job", 1),
		s3.TextractRawKey("job"),
		s3.TextractCallbackKey("job"),
		s3.BedrockPageResultKey("job", 1),
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

	tests := map[string]struct {
		key     string
		want    string
		wantErr bool
	}{
		"正常系_uploads 配下のキーの場合_jobID が取り出せること":    {key: "uploads/job-1/original.pdf", want: "job-1"},
		"異常系_work 配下のキーの場合_エラーになること":             {key: "work/job-1/text/layer.txt", wantErr: true},
		"異常系_outputs 配下のキーの場合_エラーになること":          {key: "outputs/job-1/comparison.json", wantErr: true},
		"異常系_uploads 配下でもオブジェクト名が異なる場合_エラーになること": {key: "uploads/job-1/other.pdf", wantErr: true},
		"異常系_階層が深すぎる場合_エラーになること":                 {key: "uploads/job-1/nested/original.pdf", wantErr: true},
		"境界値_jobID が空の場合_エラーになること":               {key: "uploads//original.pdf", wantErr: true},
		"境界値_キーが空文字の場合_エラーになること":                 {key: "", wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
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
