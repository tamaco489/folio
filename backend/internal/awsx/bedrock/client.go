package bedrock

// Client は awsx 層が公開する Bedrock Runtime クライアント
type Client struct {
	api            ConverseAPI
	defaultModelID string
	retry          RetryConfig
	sleep          Sleeper
	randN          func(int64) int64
}

// Option は Client の設定を差し替える
type Option func(*Client)

// WithDefaultModelID は Request でモデル ID が省略された場合の既定値を設定する
//
// モデル ID は internal/config から渡す前提であり、本パッケージには持たない
func WithDefaultModelID(id string) Option {
	return func(c *Client) { c.defaultModelID = id }
}

// WithRetry はリトライ設定を差し替える
func WithRetry(rc RetryConfig) Option {
	return func(c *Client) { c.retry = rc }
}

// WithSleeper は待機処理を差し替える (テストで実時間を待たないために用いる)
func WithSleeper(s Sleeper) Option {
	return func(c *Client) {
		if s != nil {
			c.sleep = s
		}
	}
}

// WithRandN はジッタの乱数源を差し替える (テストで待機時間を決定的にするために用いる)
func WithRandN(f func(int64) int64) Option {
	return func(c *Client) {
		if f != nil {
			c.randN = f
		}
	}
}

// New は SDK のクライアントをラップする
func New(api ConverseAPI, opts ...Option) *Client {
	c := &Client{
		api:   api,
		retry: DefaultRetryConfig(),
		sleep: RealSleeper,
		randN: defaultRandN,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
