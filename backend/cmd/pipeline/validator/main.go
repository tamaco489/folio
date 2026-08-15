// Command validator は Step Functions の入口で入力の妥当性判定と冪等性チェックを担う
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/config"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
	"github.com/tamaco489/folio/backend/internal/pipeline/validate"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load(config.RequireDocumentsBucket, config.RequireJobsTable)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	awsCfg, err := cfg.LoadAWS(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	handler := validate.New(
		s3.New(awss3.NewFromConfig(awsCfg), cfg.DocumentsBucket),
		dynamo.New(dynamodb.NewFromConfig(awsCfg), cfg.JobsTable),
		pdf.NewRunner(),
	)

	lambda.Start(handler.Handle)
}
