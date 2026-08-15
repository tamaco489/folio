// Package bedrock は Bedrock Runtime の操作を awsx 層に閉じ込める
package bedrock

import awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

// Client は awsx 層が公開する Bedrock Runtime クライアント
type Client struct {
	api *awsbedrockruntime.Client
}

// New は SDK のクライアントをラップする
func New(api *awsbedrockruntime.Client) *Client {
	return &Client{api: api}
}
