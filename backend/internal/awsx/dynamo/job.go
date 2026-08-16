package dynamo

import (
	"fmt"
	"time"

	awsdynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Status はジョブの現在状態
type Status string

const (
	StatusProcessing    Status = "PROCESSING"
	StatusCompleted     Status = "COMPLETED"
	StatusReviewPending Status = "REVIEW_PENDING"
	StatusFailed        Status = "FAILED"
)

// Valid は定義済みの状態かどうかを返す
func (s Status) Valid() bool {
	switch s {
	case StatusProcessing, StatusCompleted, StatusReviewPending, StatusFailed:
		return true
	default:
		return false
	}
}

// Reprocessable は同じ jobId の再投入で処理をやり直してよいかを返す
func (s Status) Reprocessable() bool {
	return s == StatusFailed
}

// 属性名は設計で確定した 6 つに限定する
const (
	attrJobID       = "jobId"
	attrStatus      = "status"
	attrFilename    = "filename"
	attrCreatedAt   = "createdAt"
	attrUpdatedAt   = "updatedAt"
	attrErrorReason = "errorReason"
)

// timeFormat は GSI のソートキーを辞書順で正しく比較できるよう桁数を固定する
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Job は dev-folio-jobs の 1 レコード
type Job struct {
	JobID       string
	Status      Status
	Filename    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ErrorReason string
}

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeFormat, s) }

func jobKey(jobID string) map[string]awsdynamodbtypes.AttributeValue {
	return map[string]awsdynamodbtypes.AttributeValue{
		attrJobID: &awsdynamodbtypes.AttributeValueMemberS{Value: jobID},
	}
}

func (j Job) item() (map[string]awsdynamodbtypes.AttributeValue, error) {
	if j.JobID == "" {
		return nil, fmt.Errorf("dynamo: jobId is empty")
	}
	if !j.Status.Valid() {
		return nil, fmt.Errorf("dynamo: unknown status %q", j.Status)
	}
	item := map[string]awsdynamodbtypes.AttributeValue{
		attrJobID:     &awsdynamodbtypes.AttributeValueMemberS{Value: j.JobID},
		attrStatus:    &awsdynamodbtypes.AttributeValueMemberS{Value: string(j.Status)},
		attrFilename:  &awsdynamodbtypes.AttributeValueMemberS{Value: j.Filename},
		attrCreatedAt: &awsdynamodbtypes.AttributeValueMemberS{Value: formatTime(j.CreatedAt)},
		attrUpdatedAt: &awsdynamodbtypes.AttributeValueMemberS{Value: formatTime(j.UpdatedAt)},
	}
	if j.ErrorReason != "" {
		item[attrErrorReason] = &awsdynamodbtypes.AttributeValueMemberS{Value: j.ErrorReason}
	}
	return item, nil
}

func jobFromItem(item map[string]awsdynamodbtypes.AttributeValue) (Job, error) {
	if len(item) == 0 {
		return Job{}, fmt.Errorf("dynamo: empty item")
	}
	jobID, err := stringAttr(item, attrJobID, true)
	if err != nil {
		return Job{}, err
	}
	status, err := stringAttr(item, attrStatus, true)
	if err != nil {
		return Job{}, err
	}
	filename, err := stringAttr(item, attrFilename, false)
	if err != nil {
		return Job{}, err
	}
	createdAtRaw, err := stringAttr(item, attrCreatedAt, true)
	if err != nil {
		return Job{}, err
	}
	updatedAtRaw, err := stringAttr(item, attrUpdatedAt, true)
	if err != nil {
		return Job{}, err
	}
	errorReason, err := stringAttr(item, attrErrorReason, false)
	if err != nil {
		return Job{}, err
	}
	createdAt, err := parseTime(createdAtRaw)
	if err != nil {
		return Job{}, fmt.Errorf("dynamo: parse %s: %w", attrCreatedAt, err)
	}
	updatedAt, err := parseTime(updatedAtRaw)
	if err != nil {
		return Job{}, fmt.Errorf("dynamo: parse %s: %w", attrUpdatedAt, err)
	}
	return Job{
		JobID:       jobID,
		Status:      Status(status),
		Filename:    filename,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		ErrorReason: errorReason,
	}, nil
}

func stringAttr(item map[string]awsdynamodbtypes.AttributeValue, name string, required bool) (string, error) {
	av, ok := item[name]
	if !ok {
		if required {
			return "", fmt.Errorf("dynamo: attribute %s is missing", name)
		}
		return "", nil
	}
	s, ok := av.(*awsdynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", fmt.Errorf("dynamo: attribute %s is not a string", name)
	}
	return s.Value, nil
}
