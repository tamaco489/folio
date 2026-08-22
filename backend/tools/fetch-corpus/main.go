// Command fetch-corpus は評価用論文を arXiv から取得し、ページ数・LaTeX ソースの有無・ライセンスを corpus.json に記録する
//
// デプロイ対象ではない (cmd/ ではなく tools/ に置き、just build の対象にしない)
// 取得した PDF は再配布できないため、既定の出力先 testdata/pdf/ は git 管理外にしてある
//
// 使い方 (backend/ で実行)
//
//	go run ./tools/fetch-corpus                              # cs.CL / cs.LG の新着から 8〜20 ページの論文を 5 本
//	go run ./tools/fetch-corpus -ids 2608.20318,2301.07041   # ID を直接指定 (難所を含む論文を目視で選ぶとき)
//	go run ./tools/fetch-corpus -out ../tmp/papers -want 3   # 出力先と本数を変える
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/tamaco489/folio/backend/internal/corpus"
)

func main() {
	var (
		out        = flag.String("out", "testdata/pdf", "PDF と corpus.json の出力先")
		query      = flag.String("query", corpus.DefaultQuery, "arXiv API の検索条件 (-ids を指定したときは使わない)")
		ids        = flag.String("ids", "", "取得する arXiv ID (カンマ区切り。指定すると検索しない)")
		maxResults = flag.Int("max-results", 50, "検索で集める候補の数")
		want       = flag.Int("want", 5, "条件に合う論文を何本まで取るか (0 なら候補をすべて)")
		minPages   = flag.Int("min-pages", 8, "ページ数の下限")
		maxPages   = flag.Int("max-pages", 20, "ページ数の上限 (0 なら上限なし)")
		interval   = flag.Duration("interval", corpus.DefaultInterval, "arXiv へのリクエスト間隔")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	f := &corpus.Fetcher{Interval: *interval}
	opts := corpus.Options{
		OutDir:     *out,
		Query:      *query,
		MaxResults: *maxResults,
		Want:       *want,
		MinPages:   *minPages,
		MaxPages:   *maxPages,
	}
	for id := range strings.SplitSeq(*ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			opts.IDs = append(opts.IDs, id)
		}
	}

	started := time.Now()
	c, err := f.Run(ctx, opts)
	if err != nil {
		log.Fatalf("fetch-corpus: %v", err)
	}
	fmt.Printf("記録 %d 本 (%s/%s、%s)\n", len(c.Papers), *out, corpus.RecordFile, time.Since(started).Round(time.Second))
	for _, p := range c.Papers {
		fmt.Printf("  %s%s  %2d ページ  参考文献 %3d  ソース %-5v  %-7s  %s\n", p.ArxivID, p.Version, p.Pages, p.References, p.HasSource, p.LicenseKind, p.Title)
	}
}
