package textract

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// CompletionNotification は Textract が SNS に発行する完了通知の本文
type CompletionNotification struct {
	JobID            string          `json:"JobId"`
	Status           types.JobStatus `json:"Status"`
	API              string          `json:"API"`
	JobTag           string          `json:"JobTag,omitempty"`
	Timestamp        int64           `json:"Timestamp,omitempty"`
	DocumentLocation NotifiedS3      `json:"DocumentLocation"`
}

// NotifiedS3 は完了通知に含まれる入力ドキュメントの位置
type NotifiedS3 struct {
	Bucket string `json:"S3Bucket"`
	Name   string `json:"S3ObjectName"`
}

// Succeeded は結果を取得してよい状態かを返す
// PARTIAL_SUCCESS でも Block は取得できるため成功として扱う
func (n CompletionNotification) Succeeded() bool {
	return n.Status == types.JobStatusSucceeded || n.Status == types.JobStatusPartialSuccess
}

// Time は通知のタイムスタンプを時刻に変換する
func (n CompletionNotification) Time() time.Time {
	return time.UnixMilli(n.Timestamp).UTC()
}

// ParseCompletionNotification は SNS メッセージ本文を解析する
// 得られたジョブ ID をそのまま GetDocumentAnalysis に渡せる
func ParseCompletionNotification(message []byte) (*CompletionNotification, error) {
	var n CompletionNotification
	if err := json.Unmarshal(message, &n); err != nil {
		return nil, fmt.Errorf("textract: parse completion notification: %w", err)
	}
	if n.JobID == "" {
		return nil, fmt.Errorf("%w: completion notification has no job id", ErrInvalidInput)
	}
	return &n, nil
}
