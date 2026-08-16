// Command finalizer は Stage 3 の出口で正規化・検証・成果物の永続化と DynamoDB の状態更新を担う
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tamaco489/folio/backend/internal/awsx/dynamo"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/config"
	"github.com/tamaco489/folio/backend/internal/pipeline/finalize"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify"
	"github.com/tamaco489/folio/backend/internal/pipeline/verify/crossref"
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

	// Crossref の連絡先は任意で、未設定なら public pool で動く
	handler := finalize.New(
		s3.New(awss3.NewFromConfig(awsCfg), cfg.DocumentsBucket),
		dynamo.New(awsdynamodb.NewFromConfig(awsCfg), cfg.JobsTable),
		verify.New(crossref.New(crossref.WithMailto(cfg.CrossrefMailto))),
	)

	lambda.Start(handler.Handle)
}
