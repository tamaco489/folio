package bedrockroute

import (
	"errors"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
)

var (
	// ErrInvalidPage はページ番号が 1 未満の場合に返る
	ErrInvalidPage = errors.New("bedrockroute: page number must be 1 or greater")

	// ErrEmptyImage はページ画像が空の場合に返る
	ErrEmptyImage = errors.New("bedrockroute: page image is empty")

	// ErrPageDecode はモデルの応答をページ結果として解釈できない場合に返る
	//
	// この層ではリトライしない — awsx/bedrock のリトライはスロットリング向けであり、解釈できない応答を送り直すかは呼び出し側が決める
	ErrPageDecode = errors.New("bedrockroute: response is not a valid page result")
)

// DecodeError はモデルの応答をページ結果として解釈できなかったことを表し、何が返ったかを後から確かめられるよう生の応答を保持する
//
// ErrPageDecode を包むため、呼び出し側は errors.Is での判定を変えずに errors.AsType で応答を取り出せる
type DecodeError struct {
	Page     int
	Response *bedrock.Response // Response は解釈に失敗した応答 (tool use の入力を含む)
	Err      error
}

func (e *DecodeError) Error() string { return e.Err.Error() }

func (e *DecodeError) Unwrap() error { return e.Err }
