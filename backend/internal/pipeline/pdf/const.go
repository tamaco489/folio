package pdf

import "time"

const (
	// DefaultBinDir は Lambda Layer がバイナリを展開する位置
	DefaultBinDir = "/opt/bin"

	// EnvBinDir はバイナリの配置先を上書きする環境変数
	EnvBinDir = "FOLIO_POPPLER_BIN_DIR"

	// EnvDPI はラスタライズ解像度を上書きする環境変数
	EnvDPI = "FOLIO_RASTERIZE_DPI"

	// DefaultDPI は経路 B に渡すページ画像の解像度
	//
	// 精度とコストの初期値として 150 DPI を採る (#34 の検証で見直す)
	//   - A4 (210x297mm) を 150 DPI で描画すると 1240x1754 px となり、Claude の高解像度入力の上限 (長辺 2576 px) に収まるため縮小されない
	//   - 200 DPI にすると画素数が約 1.8 倍になり画像トークンも比例して増える
	DefaultDPI = 150

	// DefaultTimeout は外部プロセス 1 回あたりの上限
	//
	// Lambda の実行時間上限 15 分を超えないよう余裕を持たせる
	DefaultTimeout = 10 * time.Minute

	// DefaultMinCharsPerPage はテキストレイヤーを持つと判定する 1 ページあたりの文字数
	//
	// スキャン PDF にもページ番号やスタンプ由来の数文字が乗ることがあるため、ゼロ判定ではなく閾値を置く
	// 通常の本文ページは 1000 文字を超えるので 50 文字は十分に低い
	DefaultMinCharsPerPage = 50

	binPDFInfo   = "pdfinfo"
	binPDFToPPM  = "pdftoppm"
	binPDFToText = "pdftotext"
)
