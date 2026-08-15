// Package textract は Textract の操作を awsx 層に閉じ込める
package textract

import awstextract "github.com/aws/aws-sdk-go-v2/service/textract"

// Client は awsx 層が公開する Textract クライアント
type Client struct {
	api *awstextract.Client
}

// New は SDK のクライアントをラップする
func New(api *awstextract.Client) *Client {
	return &Client{api: api}
}
