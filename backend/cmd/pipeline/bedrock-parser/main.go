// Command bedrock-parser は経路 B のページ単位の抽出 State を担う (Step Functions の Map から 1 ページ = 1 起動で並列に走る)
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/config"
	"github.com/tamaco489/folio/backend/internal/pipeline/bedrockparser"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/bedrockroute"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load(config.RequireDocumentsBucket, config.RequireBedrockModelID)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	awsCfg, err := cfg.LoadAWS(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	// Map の並列起動で発生するスロットリングは awsx/bedrock の既定の指数バックオフで吸収する
	handler := bedrockparser.New(
		s3.New(awss3.NewFromConfig(awsCfg), cfg.DocumentsBucket),
		bedrockroute.New(bedrock.New(bedrockruntime.NewFromConfig(awsCfg)), cfg.BedrockModelID),
	)

	lambda.Start(handler.Handle)
}
