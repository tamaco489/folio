package s3

// Client は awsx 層が公開する S3 クライアント
type Client struct {
	api    API
	bucket string
}

// New は SDK のクライアントをラップする
func New(api API, bucket string) *Client {
	return &Client{api: api, bucket: bucket}
}

// Bucket は操作対象のバケット名を返す
func (c *Client) Bucket() string {
	return c.bucket
}
