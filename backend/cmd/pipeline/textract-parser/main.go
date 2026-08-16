// Command textract-parser は経路 A (Textract → Bedrock) の起動と完了通知の処理を担う
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	awstextract "github.com/aws/aws-sdk-go-v2/service/textract"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/awsx/sfn"
	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/config"
	"github.com/tamaco489/folio/backend/internal/pipeline/extract/textractroute"
	"github.com/tamaco489/folio/backend/internal/pipeline/textractparser"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load(
		config.RequireDocumentsBucket,
		config.RequireBedrockModelID,
		config.RequireTextractSNSTopicARN,
		config.RequireTextractRoleARN,
	)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// FeatureTypes は #34 の検証で差し替えるため環境変数から受け取る
	features, err := textractparser.ParseFeatureTypes(cfg.TextractFeatureTypes)
	if err != nil {
		log.Fatalf("parse %s: %v", config.EnvKeyTextractFeatureTypes, err)
	}

	awsCfg, err := cfg.LoadAWS(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	handler := textractparser.New(
		s3.New(awss3.NewFromConfig(awsCfg), cfg.DocumentsBucket),
		textract.New(awstextract.NewFromConfig(awsCfg)),
		sfn.New(awssfn.NewFromConfig(awsCfg)),
		textractroute.New(bedrock.New(awsbedrockruntime.NewFromConfig(awsCfg)), cfg.BedrockModelID),
		textractparser.Analysis{
			SNSTopicARN:  cfg.TextractSNSTopicARN,
			RoleARN:      cfg.TextractRoleARN,
			FeatureTypes: features,
		},
	)

	lambda.Start(handler.Handle)
}
