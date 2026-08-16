package bedrockroute

import "errors"

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
