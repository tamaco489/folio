// Command preprocessor は Step Functions の前処理 State を担う
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/config"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
	"github.com/tamaco489/folio/backend/internal/pipeline/preprocess"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load(config.RequireDocumentsBucket)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	awsCfg, err := cfg.LoadAWS(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	// poppler の配置は Layer の /opt/bin と環境変数から Runner が解決する
	handler := preprocess.New(
		s3.New(awss3.NewFromConfig(awsCfg), cfg.DocumentsBucket),
		pdf.NewRunner(),
	)

	lambda.Start(handler.Handle)
}
