package dynamo

import "time"

// Client は awsx 層が公開する DynamoDB クライアント
type Client struct {
	api       API
	tableName string
	now       func() time.Time
}

// Option は Client の任意設定
type Option func(*Client)

// WithClock は時刻の取得方法を差し替える (テストで固定時刻を与えるために使う)
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// New は SDK のクライアントをラップする
func New(api API, tableName string, opts ...Option) *Client {
	c := &Client{
		api:       api,
		tableName: tableName,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// TableName は操作対象のテーブル名を返す
func (c *Client) TableName() string { return c.tableName }
