package validate

import "fmt"

// Decision は Step Functions の Choice が分岐に使う判定結果
type Decision string

const (
	// DecisionProceed は検証を通過し、新規のジョブとして後続に進めることを表す
	DecisionProceed Decision = "PROCEED"

	// DecisionSkipped は同じ jobId の処理が既にあるため何もしないことを表す
	DecisionSkipped Decision = "SKIPPED"

	// DecisionRejected は入力が要件を満たさず処理を打ち切ることを表す
	DecisionRejected Decision = "REJECTED"
)

// Code は処理を進めなかった理由の種別
type Code string

const (
	CodeNotPDF           Code = "NOT_PDF"
	CodeTooLarge         Code = "TOO_LARGE"
	CodeDamaged          Code = "DAMAGED"
	CodeEncrypted        Code = "ENCRYPTED"
	CodeTooManyPages     Code = "TOO_MANY_PAGES"
	CodeHashMismatch     Code = "HASH_MISMATCH"
	CodeInProgress       Code = "IN_PROGRESS"
	CodeAlreadyProcessed Code = "ALREADY_PROCESSED"
)

// Reason は処理を進めなかった理由
//
// State 間の 256KB 上限に収めるため、実体ではなく種別と短い説明だけを載せる
type Reason struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// errorReason は DynamoDB に記録する失敗理由を組み立てる
func (r Reason) errorReason() string {
	return fmt.Sprintf("%s: %s", r.Code, r.Message)
}
