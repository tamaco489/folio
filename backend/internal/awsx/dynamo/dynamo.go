// Package dynamo は DynamoDB の操作を awsx 層に閉じ込める
package dynamo

import awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"

// Client は awsx 層が公開する DynamoDB クライアント
type Client struct {
	api *awsdynamodb.Client
}

// New は SDK のクライアントをラップする
func New(api *awsdynamodb.Client) *Client {
	return &Client{api: api}
}
