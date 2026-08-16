// Package config は Lambda に渡される設定値の読み込みと検証を担う
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// 環境変数のキー
//
// AWS_REGION は Lambda ランタイムが予約変数として自動設定するため、独自の接頭辞を付けない
const (
	EnvKeyEnvironment          = "FOLIO_ENV"
	EnvKeyRegion               = "AWS_REGION"
	EnvKeyDocumentsBucket      = "FOLIO_DOCUMENTS_BUCKET"
	EnvKeyJobsTable            = "FOLIO_JOBS_TABLE"
	EnvKeyBedrockModelID       = "FOLIO_BEDROCK_MODEL_ID"
	EnvKeyTextractSNSTopicARN  = "FOLIO_TEXTRACT_SNS_TOPIC_ARN"
	EnvKeyTextractRoleARN      = "FOLIO_TEXTRACT_ROLE_ARN"
	EnvKeyTextractFeatureTypes = "FOLIO_TEXTRACT_FEATURE_TYPES"
	EnvKeyCrossrefMailto       = "FOLIO_CROSSREF_MAILTO"
)

// DefaultRegion は AWS_REGION が未設定のときに用いるリージョン
//
// Bedrock の上位モデルと arXiv のバルクバケットが揃う us-east-1 に固定している
const DefaultRegion = "us-east-1"

// DefaultTextractFeatureTypes は FOLIO_TEXTRACT_FEATURE_TYPES が未設定のときに用いる FeatureTypes (カンマ区切り)
//
// LAYOUT + TABLES は暫定であり、#34 の実測で差し替えるため環境変数で上書きできるようにしている
const DefaultTextractFeatureTypes = "LAYOUT,TABLES"

// Env は環境識別子
type Env string

const (
	EnvDev Env = "dev"
	EnvStg Env = "stg"
	EnvPrd Env = "prd"
)

// Valid は環境識別子が想定値かを返す
func (e Env) Valid() bool {
	switch e {
	case EnvDev, EnvStg, EnvPrd:
		return true
	default:
		return false
	}
}

// Requirement は Lambda ごとに必須とする環境変数を表す
//
// 5 つの Lambda は必要とする設定値が異なるため、構造体は 1 つに保ち、何を必須とするかだけを呼び出し側が宣言する
type Requirement string

const (
	RequireDocumentsBucket     Requirement = EnvKeyDocumentsBucket
	RequireJobsTable           Requirement = EnvKeyJobsTable
	RequireBedrockModelID      Requirement = EnvKeyBedrockModelID
	RequireTextractSNSTopicARN Requirement = EnvKeyTextractSNSTopicARN
	RequireTextractRoleARN     Requirement = EnvKeyTextractRoleARN
)

// エラーの判別用センチネル
var (
	// ErrMissingEnv は必須の環境変数が設定されていないことを表す
	ErrMissingEnv = errors.New("required environment variable is not set")

	// ErrInvalidEnv は環境変数の値が想定外であることを表す
	ErrInvalidEnv = errors.New("environment variable has an unexpected value")

	// ErrUnknownRequirement は未知の Requirement が渡されたことを表す
	ErrUnknownRequirement = errors.New("unknown requirement")
)

// Config は全 Lambda で共有する設定値
//
// S3 のキーは jobId から規則で導出できるため、プレフィックスごとの設定値は持たない
type Config struct {
	Env                  Env    // Env は環境識別子 (リソース名の接頭辞 {環境}-folio- に対応する)
	Region               string // Region は AWS リージョン
	DocumentsBucket      string // DocumentsBucket は PDF・中間データ・成果物を置く S3 バケット名
	JobsTable            string // JobsTable は処理状態と冪等性を管理する DynamoDB テーブル名
	BedrockModelID       string // BedrockModelID は構造化に用いる Bedrock のモデル ID
	TextractSNSTopicARN  string // TextractSNSTopicARN は Textract が完了通知を発行する SNS トピックの ARN
	TextractRoleARN      string // TextractRoleARN は Textract が SNS へ発行するために引き受ける IAM ロールの ARN
	TextractFeatureTypes string // TextractFeatureTypes は Textract の FeatureTypes (カンマ区切り。未設定なら DefaultTextractFeatureTypes)
	CrossrefMailto       string // CrossrefMailto は Crossref の polite pool に入るための連絡先 (任意で、未設定なら public pool で動く)
}

// Load は環境変数から設定を読み込み、required に挙げた項目が揃っているかを検証する
//
// 環境識別子とリージョンは全 Lambda が用いるため required の指定によらず常に検証する
// 欠落は 1 件目で打ち切らず、すべて集約して返す
func Load(required ...Requirement) (Config, error) {
	cfg := Config{
		Env:                  Env(lookup(EnvKeyEnvironment)),
		Region:               lookup(EnvKeyRegion),
		DocumentsBucket:      lookup(EnvKeyDocumentsBucket),
		JobsTable:            lookup(EnvKeyJobsTable),
		BedrockModelID:       lookup(EnvKeyBedrockModelID),
		TextractSNSTopicARN:  lookup(EnvKeyTextractSNSTopicARN),
		TextractRoleARN:      lookup(EnvKeyTextractRoleARN),
		TextractFeatureTypes: lookup(EnvKeyTextractFeatureTypes),
		CrossrefMailto:       lookup(EnvKeyCrossrefMailto),
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if cfg.TextractFeatureTypes == "" {
		cfg.TextractFeatureTypes = DefaultTextractFeatureTypes
	}

	var problems []error

	switch {
	case cfg.Env == "":
		problems = append(problems, fmt.Errorf("%w: %s", ErrMissingEnv, EnvKeyEnvironment))
	case !cfg.Env.Valid():
		problems = append(problems, fmt.Errorf("%w: %s=%q (want one of %q, %q, %q)",
			ErrInvalidEnv, EnvKeyEnvironment, cfg.Env, EnvDev, EnvStg, EnvPrd))
	}

	values := map[Requirement]string{
		RequireDocumentsBucket:     cfg.DocumentsBucket,
		RequireJobsTable:           cfg.JobsTable,
		RequireBedrockModelID:      cfg.BedrockModelID,
		RequireTextractSNSTopicARN: cfg.TextractSNSTopicARN,
		RequireTextractRoleARN:     cfg.TextractRoleARN,
	}
	for _, r := range required {
		value, known := values[r]
		if !known {
			problems = append(problems, fmt.Errorf("%w: %q", ErrUnknownRequirement, string(r)))
			continue
		}
		if value == "" {
			problems = append(problems, fmt.Errorf("%w: %s", ErrMissingEnv, string(r)))
		}
	}

	if len(problems) > 0 {
		return Config{}, errors.Join(problems...)
	}
	return cfg, nil
}

// lookup は環境変数を取得する (空白のみの値は未設定として扱う)
func lookup(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// LoadAWS は既定のクレデンシャルチェーンから AWS の設定を読み込む
//
// リージョンは SDK の解決順に委ねず Config.Region で上書きする
// SDK 既定に任せると、AWS_REGION 未設定のローカル実行でプロファイルのリージョンが採用され、Config.Region が指す us-east-1 との乖離が黙って生じる
func (c Config) LoadAWS(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region))
}
