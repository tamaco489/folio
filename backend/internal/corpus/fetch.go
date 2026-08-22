package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

// Fetcher は arXiv への接続と poppler の実行設定を持つ
type Fetcher struct {
	Client      *http.Client                         // Client は nil なら http.DefaultClient
	APIBaseURL  string                               // APIBaseURL は検索と OAI-PMH の接続先 (空なら DefaultAPIBaseURL)
	SiteBaseURL string                               // SiteBaseURL は PDF と e-print の接続先 (空なら DefaultSiteBaseURL)
	Runner      *pdf.Runner                          // Runner は pdfinfo と pdftotext の実行に使う
	Interval    time.Duration                        // Interval はリクエストの間隔 (0 なら空けない)
	Sleep       func(context.Context, time.Duration) // Sleep は待機の差し替え口 (nil なら time.Sleep 相当)
	Now         func() time.Time                     // Now は記録の時刻 (nil なら time.Now)
	Logf        func(string, ...any)                 // Logf は進捗の出力先 (nil なら log.Printf)

	last time.Time
}

// Options は 1 回の取得の条件
type Options struct {
	OutDir     string   // OutDir は PDF と corpus.json の置き場所
	Query      string   // Query は検索条件 (IDs が空のときに使う)
	IDs        []string // IDs は直接指定する arXiv ID (指定すると検索しない)
	MaxResults int      // MaxResults は検索で集める候補の数
	Want       int      // Want は条件に合う論文を何本まで取るか
	MinPages   int      // MinPages / MaxPages はページ数の範囲 (両端を含む)
	MaxPages   int
}

// Run は候補を集め、条件に合う論文を OutDir に取得して corpus.json に記録する
//
// 記録済みの ID は飛ばす。1 本ごとに corpus.json を書き戻し、途中で止めても取得済みの分が残るようにする
func (f *Fetcher) Run(ctx context.Context, opts Options) (*Corpus, error) {
	if opts.OutDir == "" {
		return nil, fmt.Errorf("corpus: out dir is required")
	}
	corpus, err := LoadCorpus(opts.OutDir)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	if len(opts.IDs) > 0 {
		entries, err = f.Lookup(ctx, opts.IDs)
	} else {
		entries, err = f.Search(ctx, opts.Query, opts.MaxResults)
	}
	if err != nil {
		return nil, err
	}
	f.logf("候補 %d 件", len(entries))

	taken := 0
	for _, e := range entries {
		if opts.Want > 0 && taken >= opts.Want {
			break
		}
		if corpus.Has(e.ID) {
			f.logf("skip %s: 記録済み", e.VersionedID())
			continue
		}
		p, ok, err := f.fetchOne(ctx, e, opts)
		if err != nil {
			return corpus, err
		}
		if !ok {
			continue
		}
		corpus.Add(p)
		if err := corpus.Save(opts.OutDir); err != nil {
			return corpus, err
		}
		taken++
		f.logf("take %s: %d ページ, 参考文献 %d 件, ソース %v, ライセンス %s", e.VersionedID(), p.Pages, p.References, p.HasSource, p.LicenseKind)
	}
	if taken == 0 {
		f.logf("条件に合う論文はありませんでした")
	}
	return corpus, nil
}

// fetchOne は 1 本を取得して条件を判定する (条件に合わなければ PDF を消して ok=false)
func (f *Fetcher) fetchOne(ctx context.Context, e Entry, opts Options) (Paper, bool, error) {
	file := e.VersionedID() + ".pdf"
	path := filepath.Join(opts.OutDir, file)
	if err := f.FetchPDF(ctx, e.VersionedID(), path); err != nil {
		return Paper{}, false, err
	}

	info, err := f.runner().Info(ctx, path)
	if err != nil {
		return Paper{}, false, fmt.Errorf("corpus: %s: %w", e.VersionedID(), err)
	}
	if !inRange(info.Pages, opts.MinPages, opts.MaxPages) {
		f.logf("skip %s: %d ページ (範囲外)", e.VersionedID(), info.Pages)
		return Paper{}, false, os.Remove(path)
	}

	refs, err := f.countReferences(ctx, path)
	if err != nil {
		return Paper{}, false, err
	}
	hasSource, contentType, err := f.HasSource(ctx, e.VersionedID())
	if err != nil {
		return Paper{}, false, err
	}
	licenseURL, kind, err := f.License(ctx, e.ID)
	if err != nil {
		return Paper{}, false, err
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return Paper{}, false, err
	}

	return Paper{
		ArxivID:           e.ID,
		Version:           e.Version,
		Title:             e.Title,
		PrimaryCategory:   e.Primary,
		Categories:        e.Categories,
		Published:         e.Published,
		Pages:             info.Pages,
		References:        refs,
		HasSource:         hasSource,
		SourceContentType: contentType,
		License:           licenseURL,
		LicenseKind:       kind,
		File:              file,
		SHA256:            sum,
		FetchedAt:         f.now().UTC(),
	}, true, nil
}

// refPattern は参考文献の項目の先頭 ([12] の形) に合わせる
var refPattern = regexp.MustCompile(`(?m)^\s*\[(\d+)\]`)

// countReferences は pdftotext の出力から参考文献の件数を推定する
//
// 選定条件 (30 件以上) の目安に使うだけなので、[n] 形式の最大番号を返す。番号なしの書式では 0 になる
func (f *Fetcher) countReferences(ctx context.Context, pdfPath string) (int, error) {
	tmp, err := os.CreateTemp("", "folio-corpus-*.txt")
	if err != nil {
		return 0, fmt.Errorf("corpus: create temp: %w", err)
	}
	tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := f.runner().ExtractText(ctx, pdfPath, tmp.Name()); err != nil {
		return 0, fmt.Errorf("corpus: %s: %w", filepath.Base(pdfPath), err)
	}
	text, err := os.ReadFile(tmp.Name())
	if err != nil {
		return 0, fmt.Errorf("corpus: read text: %w", err)
	}
	return CountReferences(string(text)), nil
}

// CountReferences は [n] 形式の参考文献の最大番号を返す
func CountReferences(text string) int {
	n := 0
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		if v, err := strconv.Atoi(m[1]); err == nil && v > n && v < 10000 {
			n = v
		}
	}
	return n
}

func inRange(pages, lo, hi int) bool {
	if lo > 0 && pages < lo {
		return false
	}
	if hi > 0 && pages > hi {
		return false
	}
	return true
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("corpus: read %s: %w", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// throttle は前回のリクエストから Interval が経つまで待つ
func (f *Fetcher) throttle(ctx context.Context) {
	if f.Interval <= 0 {
		return
	}
	if wait := f.Interval - f.now().Sub(f.last); wait > 0 && !f.last.IsZero() {
		f.sleep(ctx, wait)
	}
	f.last = f.now()
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

func (f *Fetcher) apiBase() string {
	return strings.TrimSuffix(orDefault(f.APIBaseURL, DefaultAPIBaseURL), "/")
}

func (f *Fetcher) siteBase() string {
	return strings.TrimSuffix(orDefault(f.SiteBaseURL, DefaultSiteBaseURL), "/")
}

func (f *Fetcher) runner() *pdf.Runner {
	if f.Runner == nil {
		f.Runner = pdf.NewRunner()
	}
	return f.Runner
}

func (f *Fetcher) sleep(ctx context.Context, d time.Duration) {
	if f.Sleep != nil {
		f.Sleep(ctx, d)
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Fetcher) logf(format string, args ...any) {
	if f.Logf != nil {
		f.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
