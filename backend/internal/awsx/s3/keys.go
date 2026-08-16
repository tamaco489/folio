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
	objectOriginalPDF      = "original.pdf"
	objectTextractRaw      = "raw.json"
	objectTextractCallback = "callback.json"
	objectTextractDocument = "document.json"
	objectTextLayer        = "layer.txt"
	objectResultTextract   = "result-textract.json"
	objectResultBedrock    = "result-bedrock.json"
	objectComparison       = "comparison.json"
	segmentPages           = "pages"
	segmentTextract        = "textract"
	segmentText            = "text"
	segmentBedrock         = "bedrock"
	pageImageNameTemplate  = "page-%04d.png"
	pageResultNameTemplate = "page-%04d.json"
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

// TextractCallbackKey は Textract の完了通知を受けて Step Functions へ応答するために要する情報 (タスクトークンなど) のキーを組み立てる
//
// タスクトークンは Textract の JobTag (64 文字上限) に収まらないため、起動側が S3 に退避し、通知を受けた側が読み戻す
func TextractCallbackKey(jobID string) string {
	return join(PrefixWork, jobID, segmentTextract, objectTextractCallback)
}

// TextractDocumentKey は textract-parser が構造化した正規化前の結果のキーを組み立てる
//
// finalizer がこれを読んで正規化・検証し、outputs/ へ最終成果物を書く
// 最終成果物 (ResultTextractKey) と分けるのは、finalizer の再実行が自分の出力を入力として読み、Textract の確信度の上書きと警告の重複で冪等でなくなるのを防ぐため
func TextractDocumentKey(jobID string) string {
	return join(PrefixWork, jobID, segmentTextract, objectTextractDocument)
}

// BedrockPageResultKey は経路 B (Bedrock) のページ単位の抽出結果のキーを組み立てる
//
// Map から 1 ページずつ書き出され、結合は Map の外で行う。ページ画像と同じ 4 桁ゼロ埋めで辞書順とページ順を揃える
func BedrockPageResultKey(jobID string, page int) string {
	return join(PrefixWork, jobID, segmentBedrock, fmt.Sprintf(pageResultNameTemplate, page))
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
