package crossref

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// testdataDir は verify パッケージの規約上の記録の配置先
func testdataDir() string {
	return filepath.Join("..", "testdata", "crossref")
}

// fakeSleeper は待機を記録するだけで実時間を消費しない
type fakeSleeper struct {
	waited []time.Duration
	err    error
}

func (s *fakeSleeper) sleep(_ context.Context, d time.Duration) error {
	s.waited = append(s.waited, d)
	return s.err
}

// fakeClock は Sleeper が呼ばれるたびに進む時計 (待った分だけ時間が経つ)
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) sleeper(s *fakeSleeper) Sleeper {
	return func(ctx context.Context, d time.Duration) error {
		c.now = c.now.Add(d)
		return s.sleep(ctx, d)
	}
}

// fakeTransport は応答の列を順に返す http.RoundTripper (記録では表せない 429 → 200 のような遷移に用いる)
type fakeTransport struct {
	requests  []*http.Request
	responses []*http.Response
	errs      []error
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	i := len(f.requests)
	f.requests = append(f.requests, req)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.responses) {
		return f.responses[i], nil
	}
	return status(http.StatusOK, `{"message":{"DOI":"10.1000/x","title":["x"]}}`), nil
}

func status(code int, body string, header ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(header); i += 2 {
		h.Set(header[i], header[i+1])
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

// newTestClient は実時間を待たず、待った分だけ進む時計を持つクライアントを組み立てる
func newTestClient(rt http.RoundTripper, opts ...Option) (*Client, *fakeSleeper) {
	sleeper := &fakeSleeper{}
	clock := &fakeClock{now: time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)}
	opts = append([]Option{WithTransport(rt), WithSleeper(clock.sleeper(sleeper)), WithClock(func() time.Time { return clock.now })}, opts...)
	return New(opts...), sleeper
}

func newReplayer(t *testing.T) *Replayer {
	t.Helper()
	r, err := NewReplayer(testdataDir())
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	return r
}

// 記録済みの応答を再生し、実 API を呼ばずに Work まで取り出せることを確かめる
func TestClientReplaysRecordings(t *testing.T) {
	c, sleeper := newTestClient(newReplayer(t))
	ctx := context.Background()

	w, err := c.LookupDOI(ctx, "10.1038/nature14539")
	if err != nil {
		t.Fatalf("LookupDOI: %v", err)
	}
	if w.DOI != "10.1038/nature14539" || w.Title != "Deep learning" {
		t.Errorf("LookupDOI = %+v", w)
	}

	// arXiv の DOI は DataCite 登録のため Crossref では 404 になる
	if _, err := c.LookupDOI(ctx, "10.48550/arxiv.2309.17453"); !errors.Is(err, ErrNotFound) {
		t.Errorf("arXiv DOI の err = %v, want ErrNotFound", err)
	}

	works, err := c.Search(ctx, "LeCun, Y., Bengio, Y., Hinton, G. Deep learning. Nature 521, 436–444 (2015).")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(works) != 3 || works[0].DOI != "10.1038/nature14539" || works[0].Title != "Deep learning" {
		t.Errorf("Search = %+v", works)
	}

	// 合成した 503 の記録: リトライを使い切って諦めること
	_, err = c.LookupDOI(ctx, "10.1000/verify-unavailable")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("503 の err = %v, want retry exhausted", err)
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Errorf("err = %v, want to mention status 503", err)
	}
	// 再生は即座に返るため 2 件目以降は最小間隔 350ms を待ち、503 の後は 1s と 2s のバックオフを挟む (バックオフで間隔が空くため直後の再送では待たない)
	want := []time.Duration{350 * time.Millisecond, 350 * time.Millisecond, 350 * time.Millisecond, time.Second, 2 * time.Second}
	if !reflect.DeepEqual(sleeper.waited, want) {
		t.Errorf("waited = %v, want %v", sleeper.waited, want)
	}
}

func TestClientRequestShape(t *testing.T) {
	ft := &fakeTransport{}
	c, _ := newTestClient(ft, WithMailto(" me@example.org "))
	ctx := context.Background()

	if _, err := c.LookupDOI(ctx, "10.1000/a;b#c"); err != nil {
		t.Fatalf("LookupDOI: %v", err)
	}
	if _, err := c.Search(ctx, "Deep learning, LeCun & Bengio"); err != nil {
		t.Fatalf("Search: %v", err)
	}

	// DOI は / ; # を含みうるためパスとしてエスケープする
	if got := ft.requests[0].URL.String(); got != "https://api.crossref.org/works/10.1000%2Fa%3Bb%23c" {
		t.Errorf("DOI の URL = %q", got)
	}
	if got := ft.requests[1].URL.String(); got != "https://api.crossref.org/works?query.bibliographic=Deep+learning%2C+LeCun+%26+Bengio&rows=3&select=DOI%2Ctitle" {
		t.Errorf("検索の URL = %q", got)
	}
	// polite pool: User-Agent に mailto を含める (クエリには載せない)
	if got := ft.requests[0].Header.Get("User-Agent"); got != "folio-verify/1.0 (https://github.com/tamaco489/folio; mailto:me@example.org)" {
		t.Errorf("User-Agent = %q", got)
	}
	if strings.Contains(ft.requests[1].URL.RawQuery, "mailto") {
		t.Errorf("クエリに mailto が載っている: %s", ft.requests[1].URL.RawQuery)
	}
}

func TestClientWithoutMailto(t *testing.T) {
	ft := &fakeTransport{}
	c, _ := newTestClient(ft)
	if _, err := c.LookupDOI(context.Background(), "10.1000/x"); err != nil {
		t.Fatalf("LookupDOI: %v", err)
	}
	if got := ft.requests[0].Header.Get("User-Agent"); got != "folio-verify/1.0 (https://github.com/tamaco489/folio)" {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestClientRetry(t *testing.T) {
	tests := map[string]struct {
		responses   []*http.Response
		errs        []error
		wantCalls   int
		wantWaits   []time.Duration
		wantErr     bool
		wantMissing bool
	}{
		"正常系_429 の後に成功する場合_待って送り直すこと": {
			responses: []*http.Response{status(http.StatusTooManyRequests, "slow down"), status(http.StatusOK, `{"message":{"DOI":"10.1000/x","title":["x"]}}`)},
			wantCalls: 2,
			wantWaits: []time.Duration{time.Second},
		},
		"正常系_転送層の失敗の後に成功する場合_送り直すこと": {
			errs:      []error{errors.New("connection reset")},
			wantCalls: 2,
			wantWaits: []time.Duration{time.Second},
		},
		"異常系_5xx が続く場合_試行回数の上限で諦めること": {
			responses: []*http.Response{status(http.StatusBadGateway, ""), status(http.StatusServiceUnavailable, ""), status(http.StatusInternalServerError, "")},
			wantCalls: 3,
			wantWaits: []time.Duration{time.Second, 2 * time.Second},
			wantErr:   true,
		},
		"異常系_403 の場合_ブロックとみなして送り直さないこと": {
			responses: []*http.Response{status(http.StatusForbidden, "blocked")},
			wantCalls: 1,
			wantErr:   true,
		},
		"異常系_400 の場合_送り直さないこと": {
			responses: []*http.Response{status(http.StatusBadRequest, `{"status":"failed"}`)},
			wantCalls: 1,
			wantErr:   true,
		},
		"異常系_404 の場合_送り直さず ErrNotFound になること": {
			responses:   []*http.Response{status(http.StatusNotFound, "Resource not found.")},
			wantCalls:   1,
			wantErr:     true,
			wantMissing: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ft := &fakeTransport{responses: tt.responses, errs: tt.errs}
			c, sleeper := newTestClient(ft)

			_, err := c.LookupDOI(context.Background(), "10.1000/x")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if errors.Is(err, ErrNotFound) != tt.wantMissing {
				t.Errorf("errors.Is(err, ErrNotFound) = %v, want %v (err = %v)", !tt.wantMissing, tt.wantMissing, err)
			}
			if len(ft.requests) != tt.wantCalls {
				t.Errorf("calls = %d, want %d", len(ft.requests), tt.wantCalls)
			}
			// バックオフで最小間隔より長く待つため、再送前の throttle は待たない
			if !reflect.DeepEqual(sleeper.waited, tt.wantWaits) {
				t.Errorf("waited = %v, want %v", sleeper.waited, tt.wantWaits)
			}
		})
	}
}

// 直列のリクエストの間に最小間隔を空け、応答のレート制限ヘッダがより長い間隔を求めればそれに従うことを確かめる
func TestClientThrottle(t *testing.T) {
	ft := &fakeTransport{responses: []*http.Response{
		status(http.StatusOK, `{"message":{}}`),
		status(http.StatusOK, `{"message":{}}`, "X-Rate-Limit-Limit", "1", "X-Rate-Limit-Interval", "1s"),
		status(http.StatusOK, `{"message":{}}`, "X-Rate-Limit-Limit", "10", "X-Rate-Limit-Interval", "1s"),
		status(http.StatusOK, `{"message":{}}`),
	}}
	sleeper := &fakeSleeper{}
	clock := &fakeClock{now: time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)}
	c := New(WithTransport(ft), WithSleeper(clock.sleeper(sleeper)), WithClock(func() time.Time { return clock.now }))
	ctx := context.Background()

	for i := range 4 {
		if _, err := c.LookupDOI(ctx, "10.1000/x"); err != nil {
			t.Fatalf("LookupDOI #%d: %v", i, err)
		}
		if i == 0 {
			// 応答に 100ms かかったことにする (最小間隔から差し引かれる)
			clock.now = clock.now.Add(100 * time.Millisecond)
		}
	}

	// 1 回目: 待たない
	// 2 回目: 既定 350ms - 経過 100ms
	// 3 回目: 2 回目の応答が 1 req/s を求めたため 1s に引き上がる
	// 4 回目: 3 回目の応答は 10 req/s (100ms) を許すが、引き上げた間隔は戻さない
	want := []time.Duration{250 * time.Millisecond, time.Second, time.Second}
	if !reflect.DeepEqual(sleeper.waited, want) {
		t.Errorf("waited = %v, want %v", sleeper.waited, want)
	}
}

func TestRateLimitInterval(t *testing.T) {
	tests := map[string]struct {
		limit, interval string
		want            time.Duration
	}{
		"正常系_10 req per 1s の場合_100ms になること": {limit: "10", interval: "1s", want: 100 * time.Millisecond},
		"正常系_50 req per 60s の場合_1.2s になること": {limit: "50", interval: "60s", want: 1200 * time.Millisecond},
		"境界値_ヘッダが無い場合_0 になること":              {want: 0},
		"境界値_Limit が 0 の場合_0 になること":         {limit: "0", interval: "1s", want: 0},
		"境界値_Interval が読めない場合_0 になること":      {limit: "10", interval: "soon", want: 0},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if tt.limit != "" {
				h.Set("X-Rate-Limit-Limit", tt.limit)
			}
			if tt.interval != "" {
				h.Set("X-Rate-Limit-Interval", tt.interval)
			}
			if got := rateLimitInterval(h); got != tt.want {
				t.Errorf("rateLimitInterval = %v, want %v", got, tt.want)
			}
		})
	}
}

// ctx のキャンセルはリトライもバックオフも待たずに抜けること
func TestClientAbortsOnCanceledContext(t *testing.T) {
	ft := &fakeTransport{}
	c, sleeper := newTestClient(ft)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.LookupDOI(ctx, "10.1000/x")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(sleeper.waited) != 0 {
		t.Errorf("waited = %v, want none", sleeper.waited)
	}
	if len(ft.requests) > 1 {
		t.Errorf("calls = %d, want at most 1", len(ft.requests))
	}
}

func TestClientDecodeErrors(t *testing.T) {
	ft := &fakeTransport{responses: []*http.Response{status(http.StatusOK, "not json"), status(http.StatusOK, `{"message":{"items":[{"DOI":"10.1000/X","title":["A","B"]}]}}`)}}
	c, _ := newTestClient(ft)
	ctx := context.Background()

	if _, err := c.LookupDOI(ctx, "10.1000/x"); err == nil {
		t.Error("JSON でない本文がエラーにならなかった")
	}
	works, err := c.Search(ctx, "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// DOI は小文字に揃え、title 配列は空白で連結する
	if len(works) != 1 || works[0].DOI != "10.1000/x" || works[0].Title != "A B" {
		t.Errorf("works = %+v", works)
	}
}
