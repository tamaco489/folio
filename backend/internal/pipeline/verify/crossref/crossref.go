// Package crossref は参照文献の外部照合に用いる Crossref REST API の呼び出しをこの層に閉じ込める
//
// 用いるのは 2 つのエンドポイントだけであり、応答も題名と DOI に落として返す
//   - GET /works/{doi}: DOI の存在確認と、登録されている題名の取得
//   - GET /works?query.bibliographic=...: DOI の無い参照文献の題名 (または生の記載) による検索
//
// 無料の公開 API であるためテストでも記録の再生で通す (Recorder / Replayer は http.RoundTripper として差し込む)
package crossref

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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

// ErrNotFound は DOI が Crossref に登録されていない場合に返る
//
// arXiv の DOI (10.48550/arXiv.*) は DataCite 登録のため Crossref では常にこれになる
var ErrNotFound = errors.New("crossref: work not found")

// Work は照合に必要な範囲に落とした Crossref の登録情報
type Work struct {
	DOI   string // DOI は Crossref が返した DOI (小文字)
	Title string // Title は登録されている題名 (title 配列を空白で連結したもの)
}

// Sleeper は待機を担う (テストで実時間を待たないよう差し替えられるようにしている)
type Sleeper func(ctx context.Context, d time.Duration) error

// RealSleeper は実時間を待つ既定の実装 (ctx のキャンセルで打ち切る)
func RealSleeper(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Option は Client の設定を差し替える
type Option func(*Client)

// WithMailto は polite pool に入るための連絡先を設定する
//
// 値は internal/config から渡す前提であり、本パッケージでは環境変数を読まない
func WithMailto(mailto string) Option {
	return func(c *Client) { c.mailto = strings.TrimSpace(mailto) }
}

// WithTransport は HTTP の転送層を差し替える (記録・再生と、テストのフェイクに用いる)
func WithTransport(rt http.RoundTripper) Option {
	return func(c *Client) { c.http.Transport = rt }
}

// WithSleeper は待機処理を差し替える (テストで実時間を待たないために用いる)
func WithSleeper(s Sleeper) Option {
	return func(c *Client) { c.sleep = s }
}

// WithClock はリクエスト間隔の計測に用いる現在時刻の取得を差し替える
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// Client は Crossref REST API のクライアント
//
// 呼び出しは直列を前提とし、同時に複数の goroutine から使わない (レート制限の間隔を 1 本の時計で数えるため)
type Client struct {
	http     *http.Client
	mailto   string
	sleep    Sleeper
	now      func() time.Time
	interval time.Duration // interval は次のリクエストまでに空ける最小間隔
	last     time.Time     // last は直前にリクエストを送った時刻
}

// New はクライアントを組み立てる
func New(opts ...Option) *Client {
	c := &Client{
		http:     &http.Client{Transport: http.DefaultTransport},
		sleep:    RealSleeper,
		now:      time.Now,
		interval: DefaultMinInterval,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// LookupDOI は DOI の登録情報を返す
//
// 未登録なら ErrNotFound、リトライしても応答が得られなければその原因のエラーを返す
func (c *Client) LookupDOI(ctx context.Context, doi string) (Work, error) {
	body, err := c.get(ctx, BaseURL+"/works/"+url.PathEscape(doi))
	if err != nil {
		return Work{}, err
	}
	var msg struct {
		Message work `json:"message"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return Work{}, fmt.Errorf("crossref: decode work: %w", err)
	}
	return msg.Message.toWork(), nil
}

// Search は書誌文字列 (題名か参照文献の記載そのもの) で検索し、関連度順の候補を返す
//
// 該当が無い場合は空のスライスを返し、エラーにしない
func (c *Client) Search(ctx context.Context, bibliographic string) ([]Work, error) {
	q := url.Values{
		"query.bibliographic": {bibliographic},
		"rows":                {strconv.Itoa(searchRows)},
		"select":              {"DOI,title"},
	}
	body, err := c.get(ctx, BaseURL+"/works?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var msg struct {
		Message struct {
			Items []work `json:"items"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("crossref: decode works: %w", err)
	}
	works := make([]Work, 0, len(msg.Message.Items))
	for _, item := range msg.Message.Items {
		works = append(works, item.toWork())
	}
	return works, nil
}

// work は Crossref の応答のうち本パッケージが読む範囲
//
// title は配列で返る (通常は 1 要素)
type work struct {
	DOI   string   `json:"DOI"`
	Title []string `json:"title"`
}

func (w work) toWork() Work {
	return Work{DOI: strings.ToLower(w.DOI), Title: strings.Join(w.Title, " ")}
}

// get は最小間隔を守りつつ GET を送り、200 の本文を返す
//
// 404 は ErrNotFound、429 と 5xx と転送層の失敗は指数バックオフでリトライし、それ以外の状態は即座にエラーにする
// 403 は Crossref によるブロックであり、送り直しても解けないためリトライしない
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}
		body, status, err := c.do(ctx, rawURL)
		switch {
		case err == nil && status == http.StatusOK:
			return body, nil
		case err == nil && status == http.StatusNotFound:
			return nil, fmt.Errorf("%w: %s", ErrNotFound, rawURL)
		case err == nil && status != http.StatusTooManyRequests && status < 500:
			return nil, fmt.Errorf("crossref: unexpected status %d: %s", status, rawURL)
		case err != nil && ctx.Err() != nil:
			return nil, err
		}

		lastErr = err
		if lastErr == nil {
			lastErr = fmt.Errorf("crossref: status %d", status)
		}
		if attempt >= maxAttempts {
			return nil, fmt.Errorf("crossref: giving up after %d attempts: %w", attempt, lastErr)
		}
		if err := c.sleep(ctx, baseDelay<<(attempt-1)); err != nil {
			return nil, fmt.Errorf("crossref: backoff interrupted: %w (last error: %w)", err, lastErr)
		}
	}
}

// throttle は直前のリクエストから最小間隔が空くまで待つ
func (c *Client) throttle(ctx context.Context) error {
	if c.last.IsZero() {
		return ctx.Err()
	}
	if wait := c.interval - c.now().Sub(c.last); wait > 0 {
		return c.sleep(ctx, wait)
	}
	return ctx.Err()
}

// do は 1 回のリクエストを送り、本文と状態コードを返す
//
// 応答のレート制限ヘッダが現在の間隔より長い間隔を求めていれば引き上げる (以後のリクエストすべてに効く)
func (c *Client) do(ctx context.Context, rawURL string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("crossref: build request: %w", err)
	}
	ua := userAgent
	if c.mailto != "" {
		ua = strings.TrimSuffix(userAgent, ")") + "; mailto:" + c.mailto + ")"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")

	c.last = c.now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("crossref: request: %w", err)
	}
	defer resp.Body.Close()

	if limit := rateLimitInterval(resp.Header); limit > c.interval {
		c.interval = limit
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("crossref: read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// rateLimitInterval は X-Rate-Limit-Limit と X-Rate-Limit-Interval から 1 リクエストあたりの間隔を求める
//
// 形式は "10" と "1s" (Interval は常に秒単位) であり、読めなければ 0 を返して既定の間隔に委ねる
func rateLimitInterval(h http.Header) time.Duration {
	limit, err := strconv.Atoi(h.Get("X-Rate-Limit-Limit"))
	if err != nil || limit <= 0 {
		return 0
	}
	interval, err := time.ParseDuration(h.Get("X-Rate-Limit-Interval"))
	if err != nil || interval <= 0 {
		return 0
	}
	return interval / time.Duration(limit)
}
