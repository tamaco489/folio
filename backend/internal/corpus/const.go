package corpus

import "time"

const (
	// DefaultAPIBaseURL は arXiv API (検索と OAI-PMH) の既定の接続先
	DefaultAPIBaseURL = "https://export.arxiv.org"

	// DefaultSiteBaseURL は PDF と e-print の既定の接続先
	DefaultSiteBaseURL = "https://arxiv.org"

	// DefaultInterval は arXiv の利用規約が求めるリクエスト間隔 (3 秒に 1 回)
	DefaultInterval = 3 * time.Second

	// DefaultQuery は選定条件のカテゴリ (cs.CL または cs.LG)
	DefaultQuery = "cat:cs.CL OR cat:cs.LG"

	// RecordFile は取得先ディレクトリに置くメタデータのファイル名
	RecordFile = "corpus.json"

	// userAgent は arXiv が求める連絡先つきの識別子
	userAgent = "folio-fetch-corpus/1 (https://github.com/tamaco489/folio)"

	// LicenseKindCC は Creative Commons のいずれか (再配布の可否はライセンスごとに違うので URL も残す)
	LicenseKindCC = "cc"

	// LicenseKindArxiv は arXiv の既定ライセンス (非独占的配布権のみ。第三者への再配布は不可)
	LicenseKindArxiv = "arxiv"

	// LicenseKindUnknown はメタデータから判定できなかった場合 (既定ライセンスと同じく再配布不可として扱う)
	LicenseKindUnknown = "unknown"
)
