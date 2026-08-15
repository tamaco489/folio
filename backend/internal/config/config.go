// Package config は Lambda に渡される設定値の読み込みと検証を担う
package config

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// LoadAWS は既定のクレデンシャルチェーンから AWS の設定を読み込む
func LoadAWS(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx)
}
