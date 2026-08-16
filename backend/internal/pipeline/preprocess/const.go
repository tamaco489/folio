package preprocess

const (
	// DefaultChunkPages は 1 回のラスタライズで /tmp に置くページ数
	//
	// Lambda の /tmp は既定 512MB であり、全ページを溜めると大きな PDF で溢れる
	// 描画したページはアップロードのたびに消すため、/tmp の占有はこの枚数分で頭打ちになる
	DefaultChunkPages = 25

	contentTypePNG  = "image/png"
	contentTypeText = "text/plain; charset=utf-8"

	fileOriginalPDF = "original.pdf"
	fileTextLayer   = "layer.txt"
	dirPages        = "pages"
	workDirPattern  = "preprocess-*"
)
