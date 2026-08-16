package sfn

// Client は awsx 層が公開する Step Functions クライアント
type Client struct {
	api API
}

// New は SDK のクライアントをラップする
func New(api API) *Client {
	return &Client{api: api}
}
