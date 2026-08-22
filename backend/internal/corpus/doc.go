// Package corpus は評価用論文を arXiv から取得し、メタデータを記録するロジックを担う (tools/fetch-corpus から呼ぶ)
//
// 処理は 3 段に分かれる
//   - Search / Lookup: arXiv API (Atom) で候補を集める
//   - Fetch: PDF を取得し、pdfinfo でページ数を判定して条件に合うものだけ残す
//   - Inspect: LaTeX ソースの有無 (e-print の Content-Type) とライセンス (OAI-PMH) を調べて記録する
//
// arXiv の API 利用規約に従い、リクエストの間隔を空ける (Fetcher.Interval)
// 取得した PDF は再配布できないため git 管理外のディレクトリに置き、記録 (corpus.json) だけを共有する
package corpus
