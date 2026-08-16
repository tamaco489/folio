package preprocess

import "errors"

var (
	// ErrEmptyJobID は入力に jobId が含まれないことを示す
	ErrEmptyJobID = errors.New("preprocess: job id is empty")

	// ErrPageCountMismatch は描画できたページ数が pdfinfo の報告と食い違うことを示す
	//
	// 後段は pageCount からページ画像のキーを導出するため、欠けたまま進めない
	ErrPageCountMismatch = errors.New("preprocess: rendered page count does not match the document")
)
