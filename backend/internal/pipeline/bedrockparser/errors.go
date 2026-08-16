package bedrockparser

import (
	"errors"

	"github.com/tamaco489/folio/backend/internal/pipeline/extract/bedrockroute"
)

var (
	// ErrEmptyJobID は入力に jobId が含まれないことを示す
	ErrEmptyJobID = errors.New("bedrockparser: job id is empty")

	// ErrInvalidPage はページ番号が 1 未満であることを示す
	ErrInvalidPage = errors.New("bedrockparser: page number must be 1 or greater")
)

// InvalidInputError は Map の 1 反復から渡された入力では処理を進められないことを表す
//
// Go の Lambda はエラーの型名を errorType として Step Functions に報告するため、再試行しても解消しない失敗を Retry から除外できるよう専用の型にする
// 存在しないページ画像 (s3.ErrNotFound) と空のページ画像もここに含める — 前処理 State がページ画像を書き終えてから Map が始まるため、無いものは再試行しても現れない
type InvalidInputError struct {
	Err error
}

func (e *InvalidInputError) Error() string { return e.Err.Error() }

func (e *InvalidInputError) Unwrap() error { return e.Err }

// PageDecodeError はモデルの応答をページ結果として解釈できなかったことを表す
//
// 同じ画像を送り直しても解消する見込みが薄いため、InvalidInputError と同じく Retry から除外できる専用の型にする (bedrock.ErrInvalidJSON と bedrockroute.ErrPageDecode を包む)
type PageDecodeError struct {
	Err error
}

func (e *PageDecodeError) Error() string { return e.Err.Error() }

func (e *PageDecodeError) Unwrap() error { return e.Err }

// classify は抽出の失敗のうち再試行しても解消しないものを専用の型へ写す
func classify(err error) error {
	switch {
	case errors.Is(err, bedrockroute.ErrPageDecode):
		return &PageDecodeError{Err: err}
	case errors.Is(err, bedrockroute.ErrEmptyImage):
		return &InvalidInputError{Err: err}
	}
	return err
}
