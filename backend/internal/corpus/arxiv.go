package corpus

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Entry は arXiv API の検索結果 1 件
type Entry struct {
	ID         string    // ID は版を除いた arXiv ID (2608.20318)
	Version    string    // Version は版 (v1)
	Title      string    // Title は改行と連続空白を畳んだ題名
	Primary    string    // Primary は主カテゴリ (cs.CL など)
	Categories []string  // Categories は主カテゴリを含む全カテゴリ
	Published  time.Time // Published は初版の投稿日時
}

// VersionedID は版つきの ID (2608.20318v1) を返す
func (e Entry) VersionedID() string { return e.ID + e.Version }

// idPattern は新形式の arXiv ID (2007 年以降) だけを受け付ける
var idPattern = regexp.MustCompile(`^(\d{4}\.\d{4,5})(v\d+)?$`)

// ParseID は arXiv ID を版の有無にかかわらず ID と版に分ける
func ParseID(s string) (id, version string, err error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "arXiv:")
	m := idPattern.FindStringSubmatch(s)
	if m == nil {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return m[1], m[2], nil
}

// feed は Atom の必要な要素だけを写した型
type feed struct {
	Entries []feedEntry `xml:"entry"`
}

type feedEntry struct {
	ID         string         `xml:"id"`
	Title      string         `xml:"title"`
	Published  string         `xml:"published"`
	Categories []feedCategory `xml:"category"`
	Primary    feedCategory   `xml:"primary_category"`
}

type feedCategory struct {
	Term string `xml:"term,attr"`
}

// Search は検索条件に合う候補を投稿日の新しい順に返す
func (f *Fetcher) Search(ctx context.Context, query string, maxResults int) ([]Entry, error) {
	q := url.Values{
		"search_query": {query},
		"start":        {"0"},
		"max_results":  {fmt.Sprint(maxResults)},
		"sortBy":       {"submittedDate"},
		"sortOrder":    {"descending"},
	}
	return f.query(ctx, q)
}

// Lookup は ID を直接指定して候補を返す (難所を含む論文を目視で選んだときに使う)
func (f *Fetcher) Lookup(ctx context.Context, ids []string) ([]Entry, error) {
	if len(ids) == 0 {
		return nil, ErrNoCandidates
	}
	q := url.Values{
		"id_list":     {strings.Join(ids, ",")},
		"max_results": {fmt.Sprint(len(ids))},
	}
	return f.query(ctx, q)
}

func (f *Fetcher) query(ctx context.Context, q url.Values) ([]Entry, error) {
	body, _, err := f.get(ctx, f.apiBase()+"/api/query?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var fd feed
	if err := xml.Unmarshal(body, &fd); err != nil {
		return nil, fmt.Errorf("corpus: parse atom feed: %w", err)
	}
	entries := make([]Entry, 0, len(fd.Entries))
	for _, e := range fd.Entries {
		entry, err := toEntry(e)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, ErrNoCandidates
	}
	return entries, nil
}

func toEntry(e feedEntry) (Entry, error) {
	// <id> は http://arxiv.org/abs/2608.20318v1 の形で来る
	id, version, err := ParseID(e.ID[strings.LastIndex(e.ID, "/")+1:])
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{
		ID:      id,
		Version: version,
		Title:   strings.Join(strings.Fields(e.Title), " "),
		Primary: e.Primary.Term,
	}
	for _, c := range e.Categories {
		if c.Term != "" {
			entry.Categories = append(entry.Categories, c.Term)
		}
	}
	if t, err := time.Parse(time.RFC3339, e.Published); err == nil {
		entry.Published = t
	}
	return entry, nil
}

// FetchPDF は PDF を path に保存する
func (f *Fetcher) FetchPDF(ctx context.Context, versionedID, path string) error {
	body, _, err := f.get(ctx, f.siteBase()+"/pdf/"+versionedID)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(body), "%PDF-") {
		return fmt.Errorf("corpus: %s: response is not a pdf", versionedID)
	}
	return writeFile(path, body)
}

// HasSource は e-print の Content-Type から LaTeX ソースの有無を判定する
//
// ソースがある投稿は application/x-eprint-tar (複数ファイル) か application/x-eprint (単一の gzip) で、PDF のみの投稿は application/pdf で返る
// 中身は使わないので HEAD で済ませ、HEAD を受け付けない場合だけ GET にする
func (f *Fetcher) HasSource(ctx context.Context, versionedID string) (bool, string, error) {
	u := f.siteBase() + "/e-print/" + versionedID
	contentType, err := f.head(ctx, u)
	if err != nil {
		_, contentType, err = f.get(ctx, u)
		if err != nil {
			return false, "", err
		}
	}
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "x-eprint") || strings.Contains(ct, "gzip") || strings.Contains(ct, "tar"), contentType, nil
}

// oaiRecord は OAI-PMH (metadataPrefix=arXiv) の必要な要素だけを写した型
type oaiRecord struct {
	License string `xml:"GetRecord>record>metadata>arXiv>license"`
}

// License は OAI-PMH のメタデータからライセンスの URL と種別を返す
//
// 既定の arXiv ライセンスの投稿は <license> が無いか nonexclusive-distrib の URL を持つ
func (f *Fetcher) License(ctx context.Context, id string) (licenseURL, kind string, err error) {
	q := url.Values{
		"verb":           {"GetRecord"},
		"identifier":     {"oai:arXiv.org:" + id},
		"metadataPrefix": {"arXiv"},
	}
	body, _, err := f.get(ctx, f.apiBase()+"/oai2?"+q.Encode())
	if err != nil {
		return "", "", err
	}
	var rec oaiRecord
	if err := xml.Unmarshal(body, &rec); err != nil {
		return "", "", fmt.Errorf("corpus: parse oai record: %w", err)
	}
	return rec.License, classifyLicense(rec.License), nil
}

// classifyLicense は URL を種別に畳む
func classifyLicense(licenseURL string) string {
	switch {
	case strings.Contains(licenseURL, "creativecommons.org"):
		return LicenseKindCC
	case strings.Contains(licenseURL, "arxiv.org/licenses"):
		return LicenseKindArxiv
	case licenseURL == "":
		return LicenseKindArxiv
	default:
		return LicenseKindUnknown
	}
}

func (f *Fetcher) get(ctx context.Context, u string) (body []byte, contentType string, err error) {
	f.throttle(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("corpus: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("corpus: get %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("%w: %d for %s", ErrUnexpectedStatus, resp.StatusCode, u)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("corpus: read %s: %w", u, err)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (f *Fetcher) head(ctx context.Context, u string) (contentType string, err error) {
	f.throttle(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return "", fmt.Errorf("corpus: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("corpus: head %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("%w: %d for %s", ErrUnexpectedStatus, resp.StatusCode, u)
	}
	return resp.Header.Get("Content-Type"), nil
}
