package s3

import (
	"fmt"
	"strings"
)

// S3 キーの第 1 階層
//
// S3 イベント通知は PrefixUploads だけをフィルタ対象にする
// 派生物を PrefixWork / PrefixOutputs に分けているのは、書き込みで通知が再発火してパイプラインが無限ループするのを防ぐため
const (
	PrefixUploads = "uploads"
	PrefixWork    = "work"
	PrefixOutputs = "outputs"
)

// MaxPageNumber は PageImageKey が扱えるページ番号の上限
//
// 非同期処理の上限が 3,000 ページであるため 3 桁では足りず、ゼロ埋めを 4 桁にしている
// これを超えると桁が増えてキーの辞書順とページ順が一致しなくなるため、バリデーション層でこの値を上限として弾く
const MaxPageNumber = 9999

const (
	objectOriginalPDF     = "original.pdf"
	objectTextractRaw     = "raw.json"
	objectTextLayer       = "layer.txt"
	objectResultTextract  = "result-textract.json"
	objectResultBedrock   = "result-bedrock.json"
	objectComparison      = "comparison.json"
	segmentPages          = "pages"
	segmentTextract       = "textract"
	segmentText           = "text"
	pageImageNameTemplate = "page-%04d.png"
)

// OriginalPDFKey は受領した PDF のキーを組み立てる
func OriginalPDFKey(jobID string) string {
	return join(PrefixUploads, jobID, objectOriginalPDF)
}

// PageImageKey はラスタライズしたページ画像のキーを組み立てる
func PageImageKey(jobID string, page int) string {
	return join(PrefixWork, jobID, segmentPages, fmt.Sprintf(pageImageNameTemplate, page))
}

// TextractRawKey は Textract の生出力のキーを組み立てる
func TextractRawKey(jobID string) string {
	return join(PrefixWork, jobID, segmentTextract, objectTextractRaw)
}

// TextLayerKey は PDF から抽出したテキストレイヤーのキーを組み立てる
func TextLayerKey(jobID string) string {
	return join(PrefixWork, jobID, segmentText, objectTextLayer)
}

// ResultTextractKey は経路 A (Textract) の抽出結果のキーを組み立てる
func ResultTextractKey(jobID string) string {
	return join(PrefixOutputs, jobID, objectResultTextract)
}

// ResultBedrockKey は経路 B (Bedrock) の抽出結果のキーを組み立てる
func ResultBedrockKey(jobID string) string {
	return join(PrefixOutputs, jobID, objectResultBedrock)
}

// ComparisonKey は両経路の差分と評価のキーを組み立てる
func ComparisonKey(jobID string) string {
	return join(PrefixOutputs, jobID, objectComparison)
}

// JobIDFromUploadKey は S3 イベントで渡されたキーから jobID を取り出す
//
// uploads/ 配下以外のキーはイベント通知の設定ミスとして扱う
func JobIDFromUploadKey(key string) (string, error) {
	segments := strings.Split(key, "/")
	if len(segments) != 3 || segments[0] != PrefixUploads || segments[2] != objectOriginalPDF {
		return "", fmt.Errorf("s3: %q is not an upload key", key)
	}
	if segments[1] == "" {
		return "", fmt.Errorf("s3: %q has an empty job id", key)
	}
	return segments[1], nil
}

func join(segments ...string) string {
	return strings.Join(segments, "/")
}
