package bedrock

import "errors"

var (
	// ErrModelIDRequired はモデル ID が Request にも既定値にも指定されていない場合に返る
	ErrModelIDRequired = errors.New("bedrock: model id is required")

	// ErrEmptyRequest はメッセージが 1 件もない場合に返る
	ErrEmptyRequest = errors.New("bedrock: request has no messages")

	// ErrEmptyContent は中身のない content block が含まれる場合に返る
	ErrEmptyContent = errors.New("bedrock: content block is empty")

	// ErrUnsupportedContent は未知の content block 種別が渡された場合に返る
	ErrUnsupportedContent = errors.New("bedrock: unsupported content block")

	// ErrInvalidToolSpec は Request.Tool に名前かスキーマが無い場合に返る
	ErrInvalidToolSpec = errors.New("bedrock: tool spec requires name and schema")

	// ErrNoTextContent はモデルの応答にテキストも tool use も含まれない場合に返る
	ErrNoTextContent = errors.New("bedrock: response has no text or tool use content")

	// ErrInvalidJSON はモデルの応答を JSON として解釈できない場合に返る
	ErrInvalidJSON = errors.New("bedrock: response is not valid json")

	// ErrOutputTruncated はモデルの生成が Request.MaxTokens で打ち切られ、応答の末尾が欠けている場合に返る
	ErrOutputTruncated = errors.New("bedrock: output truncated at max tokens")

	// ErrRetryExhausted はリトライ上限に達した場合に返る
	ErrRetryExhausted = errors.New("bedrock: retry attempts exhausted")

	// ErrRecordingNotFound は再生対象の記録が見つからない場合に返る
	ErrRecordingNotFound = errors.New("bedrock: recording not found")

	// ErrRecordKeyRequired は記録・再生時にキーが指定されていない場合に返る
	ErrRecordKeyRequired = errors.New("bedrock: record key is required")
)
