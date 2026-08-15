// Package s3 は S3 の操作を awsx 層に閉じ込める
package s3

import awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

// Client は awsx 層が公開する S3 クライアント
type Client struct {
	api *awss3.Client
}

// New は SDK のクライアントをラップする
func New(api *awss3.Client) *Client {
	return &Client{api: api}
}
