package bedrock

import (
	"context"

	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// ConverseAPI は awsbedrockruntime.Client のうち本パッケージが利用する範囲
//
// テストでフェイクに差し替えるためにインタフェースとして切り出す
type ConverseAPI interface {
	Converse(ctx context.Context, params *awsbedrockruntime.ConverseInput, optFns ...func(*awsbedrockruntime.Options)) (*awsbedrockruntime.ConverseOutput, error)
}

// Converser は経路 A と経路 B の双方が依存する呼び出し口
//
// 実クライアント、記録モード、再生モードのいずれもこれを満たす
type Converser interface {
	Converse(ctx context.Context, req Request) (*Response, error)
}

var _ Converser = (*Client)(nil)
