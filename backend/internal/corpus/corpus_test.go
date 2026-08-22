package corpus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// minimalPDF は pdfinfo がページ数を数えられる最小の PDF を組み立てる (本文は持たない)
func minimalPDF(pages int) []byte {
	var sb strings.Builder
	offsets := []int{}
	write := func(s string) {
		offsets = append(offsets, sb.Len())
		sb.WriteString(s)
	}
	sb.WriteString("%PDF-1.4\n")
	write("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	kids := make([]string, 0, pages)
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i))
	}
	write(fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), pages))
	for i := range pages {
		write(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n", 3+i))
	}
	xref := sb.Len()
	fmt.Fprintf(&sb, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&sb, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&sb, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return []byte(sb.String())
}

// newServer は arXiv の API とサイトを 1 つの httptest.Server で模す
//
// 9000.00001 (10 ページ、ソースあり、CC BY)、9000.00002 (25 ページ、範囲外)、9000.00003 (12 ページ、PDF のみ、既定ライセンス)
func newServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	atom, err := os.ReadFile(filepath.Join("testdata", "atom.xml"))
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]int{"9000.00001v2": 10, "9000.00002v1": 25, "9000.00003v1": 12}
	var requests []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/query", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(atom)
	})
	mux.HandleFunc("/pdf/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		n, ok := pages[strings.TrimPrefix(r.URL.Path, "/pdf/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(minimalPDF(n))
	})
	mux.HandleFunc("/e-print/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "9000.00003v1") {
			w.Header().Set("Content-Type", "application/pdf")
		} else {
			w.Header().Set("Content-Type", "application/x-eprint-tar")
		}
	})
	mux.HandleFunc("/oai2", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		license := ""
		if r.URL.Query().Get("identifier") == "oai:arXiv.org:9000.00001" {
			license = "<license>http://creativecommons.org/licenses/by/4.0/</license>"
		}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><OAI-PMH><GetRecord><record><metadata><arXiv><id>x</id>%s</arXiv></metadata></record></GetRecord></OAI-PMH>`, license)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &requests
}

func TestParseID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in          string
		wantID      string
		wantVersion string
		wantErr     bool
	}{
		"正常系_版つきの場合_ID と版に分かれること":            {in: "2608.20318v1", wantID: "2608.20318", wantVersion: "v1"},
		"正常系_版なしの場合_版が空になること":                {in: "2608.20318", wantID: "2608.20318"},
		"正常系_arXiv: 接頭辞と空白がある場合_取り除かれること":    {in: " arXiv:2301.07041v3 ", wantID: "2301.07041", wantVersion: "v3"},
		"異常系_旧形式の ID の場合_ErrInvalidID になること": {in: "hep-th/9901001", wantErr: true},
		"異常系_空文字の場合_ErrInvalidID になること":      {in: "", wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id, version, err := ParseID(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidID) {
					t.Fatalf("err = %v, want ErrInvalidID", err)
				}
				return
			}
			if err != nil || id != tt.wantID || version != tt.wantVersion {
				t.Errorf("ParseID(%q) = %q, %q, %v", tt.in, id, version, err)
			}
		})
	}
}

func TestClassifyLicense(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want string
	}{
		"正常系_CC BY の場合_cc になること":            {in: "http://creativecommons.org/licenses/by/4.0/", want: LicenseKindCC},
		"正常系_arXiv の既定ライセンスの場合_arxiv になること": {in: "http://arxiv.org/licenses/nonexclusive-distrib/1.0/", want: LicenseKindArxiv},
		"境界値_license が無い場合_arxiv として扱うこと":   {in: "", want: LicenseKindArxiv},
		"異常系_知らない URL の場合_unknown になること":    {in: "https://example.com/license", want: LicenseKindUnknown},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := classifyLicense(tt.in); got != tt.want {
				t.Errorf("classifyLicense(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCountReferences(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		text string
		want int
	}{
		"正常系_[n] 形式の場合_最大番号を返すこと":          {text: "References\n[1] A. Author. Title. 2020.\n[2] B. Author.\n  [3] C. Author.\n", want: 3},
		"正常系_本文中の引用 [12] がある場合_行頭だけを数えること": {text: "as shown in [12] and\n[1] A.\n[2] B.\n", want: 2},
		"境界値_番号なしの書式の場合_0 になること":           {text: "References\nAuthor, A. (2020). Title.\n", want: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := CountReferences(tt.text); got != tt.want {
				t.Errorf("CountReferences = %d, want %d", got, tt.want)
			}
		})
	}
}

// 検索 → 取得 → 判定 → 記録の経路を、フェイクの arXiv と pdfinfo で通す
func TestRunFetchesMatchingPapers(t *testing.T) {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("pdfinfo が無いため飛ばす")
	}

	srv, requests := newServer(t)
	out := t.TempDir()
	now := time.Date(2026, 8, 22, 6, 0, 0, 0, time.UTC)
	f := &Fetcher{
		Client:      srv.Client(),
		APIBaseURL:  srv.URL,
		SiteBaseURL: srv.URL,
		Now:         func() time.Time { return now },
		Logf:        t.Logf,
	}
	opts := Options{OutDir: out, Query: DefaultQuery, MaxResults: 10, Want: 5, MinPages: 8, MaxPages: 20}

	c, err := f.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(c.Papers) != 2 {
		t.Fatalf("papers = %d, want 2 (25 ページの 9000.00002 は範囲外)", len(c.Papers))
	}

	p := c.Papers[0]
	if p.ArxivID != "9000.00001" || p.Version != "v2" || p.Pages != 10 || !p.HasSource || p.LicenseKind != LicenseKindCC {
		t.Errorf("papers[0] = %+v", p)
	}
	if p.Title != "Learning to Structure Scientific Documents" {
		t.Errorf("title = %q, want 改行と連続空白を畳んだ題名", p.Title)
	}
	if p.File != "9000.00001v2.pdf" || len(p.SHA256) != 64 || !p.FetchedAt.Equal(now) {
		t.Errorf("papers[0] = %+v", p)
	}
	q := c.Papers[1]
	if q.ArxivID != "9000.00003" || q.Pages != 12 || q.HasSource || q.LicenseKind != LicenseKindArxiv || q.License != "" {
		t.Errorf("papers[1] = %+v", q)
	}

	// 範囲外の PDF は残さない
	if _, err := os.Stat(filepath.Join(out, "9000.00002v1.pdf")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("範囲外の PDF が残っている: %v", err)
	}
	for _, file := range []string{"9000.00001v2.pdf", "9000.00003v1.pdf", RecordFile} {
		if _, err := os.Stat(filepath.Join(out, file)); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}

	// corpus.json を読み直しても同じ記録が得られ、再実行は記録済みを飛ばす
	loaded, err := LoadCorpus(out)
	if err != nil || len(loaded.Papers) != 2 {
		t.Fatalf("LoadCorpus = %+v, %v", loaded, err)
	}
	before := len(*requests)
	if _, err := f.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run (2 回目): %v", err)
	}
	after := (*requests)[before:]
	for _, r := range after {
		if strings.Contains(r, "/pdf/9000.00001") || strings.Contains(r, "/pdf/9000.00003") {
			t.Errorf("記録済みの PDF を取り直している: %s", r)
		}
	}
}

func TestLookupBuildsIDList(t *testing.T) {
	srv, requests := newServer(t)
	f := &Fetcher{Client: srv.Client(), APIBaseURL: srv.URL, SiteBaseURL: srv.URL}

	entries, err := f.Lookup(context.Background(), []string{"9000.00001", "9000.00003"})
	if err != nil || len(entries) != 3 {
		t.Fatalf("Lookup = %d entries, %v", len(entries), err)
	}
	if r := (*requests)[0]; !strings.Contains(r, "id_list=9000.00001%2C9000.00003") {
		t.Errorf("request = %s, want id_list", r)
	}
	if _, err := f.Lookup(context.Background(), nil); !errors.Is(err, ErrNoCandidates) {
		t.Errorf("err = %v, want ErrNoCandidates", err)
	}
}

// リクエストの間隔を Sleep の差し替えで検証する (実時間は待たない)
func TestThrottleKeepsInterval(t *testing.T) {
	srv, _ := newServer(t)
	clock := time.Date(2026, 8, 22, 6, 0, 0, 0, time.UTC)
	var slept []time.Duration
	f := &Fetcher{
		Client:      srv.Client(),
		APIBaseURL:  srv.URL,
		SiteBaseURL: srv.URL,
		Interval:    3 * time.Second,
		Now:         func() time.Time { return clock },
		Sleep: func(_ context.Context, d time.Duration) {
			slept = append(slept, d)
			clock = clock.Add(d)
		},
	}
	ctx := context.Background()
	if _, _, err := f.License(ctx, "9000.00001"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 0 {
		t.Fatalf("初回のリクエストで待っている: %v", slept)
	}
	clock = clock.Add(time.Second)
	if _, _, err := f.License(ctx, "9000.00001"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("slept = %v, want [2s] (3 秒の間隔に 1 秒経過した分を差し引く)", slept)
	}
}

func TestGetRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	f := &Fetcher{Client: srv.Client(), APIBaseURL: srv.URL, SiteBaseURL: srv.URL}

	if _, err := f.Search(context.Background(), "x", 1); !errors.Is(err, ErrUnexpectedStatus) {
		t.Errorf("err = %v, want ErrUnexpectedStatus", err)
	}
}
