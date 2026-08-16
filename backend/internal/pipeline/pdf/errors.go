package pdf

import "errors"

var (
	// ErrBinaryNotFound は poppler の実行ファイルを解決できないことを示す
	ErrBinaryNotFound = errors.New("pdf: poppler binary not found")

	// ErrEncrypted は PDF が暗号化・保護されていることを示す
	ErrEncrypted = errors.New("pdf: document is encrypted")

	// ErrDamaged は PDF として読めないことを示す
	ErrDamaged = errors.New("pdf: document is damaged or not a pdf")

	// ErrNoPagesRendered はラスタライズ結果が 1 枚も得られないことを示す
	ErrNoPagesRendered = errors.New("pdf: no pages rendered")
)
