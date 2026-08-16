package crossref

import "time"

const (
	// BaseURL は Crossref REST API の起点
	BaseURL = "https://api.crossref.org"

	// userAgent は polite pool へ入るための識別子 (mailto は WithMailto で末尾に足す)
	//
	// Crossref は User-Agent か mailto クエリのどちらかに連絡先があれば polite pool に振り分ける
	// クエリに載せると記録の URL に連絡先が残るため User-Agent 側を使う
	userAgent = "folio-verify/1.0 (https://github.com/tamaco489/folio)"

	// searchRows は題名検索で取得する候補数
	//
	// 関連度順の先頭 3 件に無ければ Crossref に登録が無いか、記載が別物とみなす
	searchRows = 3

	// requestTimeout は 1 リクエストあたりの上限
	requestTimeout = 10 * time.Second

	// maxAttempts は 429 と 5xx に対する試行回数の上限 (初回を含む)
	//
	// 恒常的な障害に粘っても Lambda の実行時間を食うだけであり、照合できなかった参照文献は unavailable として続行する
	maxAttempts = 3

	// baseDelay は 1 回目のリトライ前の待機時間 (以降は倍にする)
	baseDelay = time.Second

	// DefaultMinInterval はリクエスト間の最小間隔
	//
	// polite pool のリスト検索は 3 req/s (2026-08 時点) であり、直列で 1 リクエストずつ送る本パッケージはこれを守れば足りる
	// 応答の X-Rate-Limit-Limit / X-Rate-Limit-Interval がより長い間隔を求める場合はその値へ引き上げる
	DefaultMinInterval = 350 * time.Millisecond

	// maxBodyBytes は応答本文の読み込み上限 (参照文献の多い記録でも数百 KB に収まる)
	maxBodyBytes = 8 << 20
)
