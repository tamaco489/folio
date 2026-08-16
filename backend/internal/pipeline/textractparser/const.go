package textractparser

// snsEventSource は SNS が Lambda に配送するイベントの EventSource (aws-lambda-go の events/testdata/sns-event.json に同じ)
const snsEventSource = "aws:sns"

// SendTaskFailure の Error に入れる失敗の種別
//
// State Machine (#30) の Catch と部分失敗の扱い (#24) はこの値で分岐する
const (
	// FailureTextractJob は Textract のジョブ自体が失敗した (文書を解析できなかった) ことを表す
	FailureTextractJob = "TextractJobFailed"

	// FailureExtract は Textract は成功したが、結果の取得・構造化・保存のいずれかが失敗したことを表す
	FailureExtract = "ExtractFailed"
)
