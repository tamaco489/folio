package validate

const (
	// MaxBytes は受け付ける PDF のサイズ上限
	//
	// Textract の非同期処理が 500MB を上限とする
	// AWS は単位を MB としか示さないため、2 進・10 進のどちらの解釈でも上限内に収まる 10 進の値を採る
	MaxBytes = 500 * 1000 * 1000

	// MaxPages は受け付ける PDF のページ数上限
	//
	// Textract の上限は 3,000 ページだが、評価対象の論文は 8 〜 20 ページであり 3,000 ページ相当の入力は想定しない
	// 前処理 (#17) のラスタライズが Lambda の実行時間 15 分に収まる範囲へ揃えるため、意図的に小さく取る
	MaxPages = 200

	// headerWindow はマジックバイトを探す先頭バイト数
	headerWindow = 1024
)

// pdfMagic は PDF ファイルの先頭に現れる署名
var pdfMagic = []byte("%PDF-")
