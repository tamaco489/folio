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
	EnvKeyEnvironment     = "FOLIO_ENV"
	EnvKeyRegion          = "AWS_REGION"
	EnvKeyDocumentsBucket = "FOLIO_DOCUMENTS_BUCKET"
	EnvKeyJobsTable       = "FOLIO_JOBS_TABLE"
	EnvKeyBedrockModelID  = "FOLIO_BEDROCK_MODEL_ID"
)

// DefaultRegion は AWS_REGION が未設定のときに用いるリージョン
//
// Bedrock の上位モデルと arXiv のバルクバケットが揃う us-east-1 に固定している
const DefaultRegion = "us-east-1"

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
// 5 つの Lambda は必要とする設定値が異なるため、構造体は 1 つに保ち、
// 何を必須とするかだけを呼び出し側が宣言する
type Requirement string

const (
	RequireDocumentsBucket Requirement = EnvKeyDocumentsBucket
	RequireJobsTable       Requirement = EnvKeyJobsTable
	RequireBedrockModelID  Requirement = EnvKeyBedrockModelID
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
	// Env は環境識別子。リソース名の接頭辞 {環境}-folio- に対応する
	Env Env
	// Region は AWS リージョン
	Region string
	// DocumentsBucket は PDF・中間データ・成果物を置く S3 バケット名
	DocumentsBucket string
	// JobsTable は処理状態と冪等性を管理する DynamoDB テーブル名
	JobsTable string
	// BedrockModelID は構造化に用いる Bedrock のモデル ID
	BedrockModelID string
}

// Load は環境変数から設定を読み込み、required に挙げた項目が揃っているかを検証する
//
// 環境識別子とリージョンは全 Lambda が用いるため required の指定によらず常に検証する
// 欠落は 1 件目で打ち切らず、すべて集約して返す
func Load(required ...Requirement) (Config, error) {
	cfg := Config{
		Env:             Env(lookup(EnvKeyEnvironment)),
		Region:          lookup(EnvKeyRegion),
		DocumentsBucket: lookup(EnvKeyDocumentsBucket),
		JobsTable:       lookup(EnvKeyJobsTable),
		BedrockModelID:  lookup(EnvKeyBedrockModelID),
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
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
		RequireDocumentsBucket: cfg.DocumentsBucket,
		RequireJobsTable:       cfg.JobsTable,
		RequireBedrockModelID:  cfg.BedrockModelID,
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

// lookup は環境変数を取得する。空白のみの値は未設定として扱う
func lookup(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// LoadAWS は既定のクレデンシャルチェーンから AWS の設定を読み込む
//
// リージョンは SDK の解決順に委ねず Config.Region で上書きする
// SDK 既定に任せると、AWS_REGION 未設定のローカル実行でプロファイルのリージョンが採用され、
// Config.Region が指す us-east-1 との乖離が黙って生じる
func (c Config) LoadAWS(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region))
}
