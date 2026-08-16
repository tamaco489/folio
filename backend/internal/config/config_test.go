package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setEnv は Load が参照する環境変数をすべて上書きする (空文字は未設定と同義に扱われるため、ホスト側の値を確実に打ち消せる)
func setEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{
		EnvKeyEnvironment,
		EnvKeyRegion,
		EnvKeyDocumentsBucket,
		EnvKeyJobsTable,
		EnvKeyBedrockModelID,
		EnvKeyTextractSNSTopicARN,
		EnvKeyTextractRoleARN,
		EnvKeyTextractFeatureTypes,
		EnvKeyCrossrefMailto,
	} {
		t.Setenv(key, values[key])
	}
}

func fullEnv() map[string]string {
	return map[string]string{
		EnvKeyEnvironment:          "dev",
		EnvKeyRegion:               "us-east-1",
		EnvKeyDocumentsBucket:      "dev-folio-documents-000000000000",
		EnvKeyJobsTable:            "dev-folio-jobs",
		EnvKeyBedrockModelID:       "anthropic.claude-sonnet-4-20250514-v1:0",
		EnvKeyTextractSNSTopicARN:  "arn:aws:sns:us-east-1:000000000000:dev-folio-textract-completion",
		EnvKeyTextractRoleARN:      "arn:aws:iam::000000000000:role/dev-folio-textract-publish-role",
		EnvKeyTextractFeatureTypes: "LAYOUT,TABLES,FORMS",
		EnvKeyCrossrefMailto:       "folio@example.com",
	}
}

func TestLoadAllValuesPresent(t *testing.T) {
	setEnv(t, fullEnv())

	cfg, err := Load(RequireDocumentsBucket, RequireJobsTable, RequireBedrockModelID, RequireTextractSNSTopicARN, RequireTextractRoleARN)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Env != EnvDev {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDev)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", cfg.Region, "us-east-1")
	}
	if cfg.DocumentsBucket != "dev-folio-documents-000000000000" {
		t.Errorf("DocumentsBucket = %q", cfg.DocumentsBucket)
	}
	if cfg.JobsTable != "dev-folio-jobs" {
		t.Errorf("JobsTable = %q", cfg.JobsTable)
	}
	if cfg.BedrockModelID != "anthropic.claude-sonnet-4-20250514-v1:0" {
		t.Errorf("BedrockModelID = %q", cfg.BedrockModelID)
	}
	if cfg.TextractSNSTopicARN != "arn:aws:sns:us-east-1:000000000000:dev-folio-textract-completion" {
		t.Errorf("TextractSNSTopicARN = %q", cfg.TextractSNSTopicARN)
	}
	if cfg.TextractRoleARN != "arn:aws:iam::000000000000:role/dev-folio-textract-publish-role" {
		t.Errorf("TextractRoleARN = %q", cfg.TextractRoleARN)
	}
	if cfg.TextractFeatureTypes != "LAYOUT,TABLES,FORMS" {
		t.Errorf("TextractFeatureTypes = %q", cfg.TextractFeatureTypes)
	}
	if cfg.CrossrefMailto != "folio@example.com" {
		t.Errorf("CrossrefMailto = %q", cfg.CrossrefMailto)
	}
}

// Crossref の連絡先は任意であり、未設定でも Load が失敗しない (public pool で動く)
func TestLoadCrossrefMailtoIsOptional(t *testing.T) {
	env := fullEnv()
	env[EnvKeyCrossrefMailto] = ""
	setEnv(t, env)

	cfg, err := Load(RequireDocumentsBucket, RequireJobsTable)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.CrossrefMailto != "" {
		t.Errorf("CrossrefMailto = %q, want empty", cfg.CrossrefMailto)
	}
}

// FeatureTypes は #34 で差し替えるまで既定値で動かすため、未設定でも Load が失敗しない
func TestLoadTextractFeatureTypesDefaults(t *testing.T) {
	env := fullEnv()
	env[EnvKeyTextractFeatureTypes] = ""
	setEnv(t, env)

	cfg, err := Load(RequireTextractSNSTopicARN, RequireTextractRoleARN)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.TextractFeatureTypes != DefaultTextractFeatureTypes {
		t.Errorf("TextractFeatureTypes = %q, want %q", cfg.TextractFeatureTypes, DefaultTextractFeatureTypes)
	}
}

func TestLoadRegionDefaults(t *testing.T) {
	env := fullEnv()
	env[EnvKeyRegion] = ""
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Region != DefaultRegion {
		t.Errorf("Region = %q, want %q", cfg.Region, DefaultRegion)
	}
}

func TestLoadEnvironmentValidation(t *testing.T) {
	tests := map[string]struct {
		value   string
		want    Env
		wantErr error
	}{
		"正常系_dev が指定された場合_EnvDev として読み込まれること": {value: "dev", want: EnvDev},
		"正常系_stg が指定された場合_EnvStg として読み込まれること": {value: "stg", want: EnvStg},
		"正常系_prd が指定された場合_EnvPrd として読み込まれること": {value: "prd", want: EnvPrd},
		"異常系_空文字の場合_ErrMissingEnv が返ること":      {value: "", wantErr: ErrMissingEnv},
		"異常系_空白のみの場合_ErrMissingEnv が返ること":     {value: "   ", wantErr: ErrMissingEnv},
		"異常系_想定外の値の場合_ErrInvalidEnv が返ること":    {value: "prod", wantErr: ErrInvalidEnv},
		"異常系_大文字の場合_ErrInvalidEnv が返ること":      {value: "DEV", wantErr: ErrInvalidEnv},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := fullEnv()
			env[EnvKeyEnvironment] = tt.value
			setEnv(t, env)

			cfg, err := Load()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Load() error = %v, want %v", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), EnvKeyEnvironment) {
					t.Errorf("error %q does not mention %q", err, EnvKeyEnvironment)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg.Env != tt.want {
				t.Errorf("Env = %q, want %q", cfg.Env, tt.want)
			}
		})
	}
}

func TestLoadMissingRequiredValue(t *testing.T) {
	tests := map[string]struct {
		missingKey  string
		requirement Requirement
	}{
		"異常系_必須のバケット名が欠落した場合_ErrMissingEnv が返ること":              {missingKey: EnvKeyDocumentsBucket, requirement: RequireDocumentsBucket},
		"異常系_必須のテーブル名が欠落した場合_ErrMissingEnv が返ること":              {missingKey: EnvKeyJobsTable, requirement: RequireJobsTable},
		"異常系_必須のモデル ID が欠落した場合_ErrMissingEnv が返ること":            {missingKey: EnvKeyBedrockModelID, requirement: RequireBedrockModelID},
		"異常系_必須の SNS トピック ARN が欠落した場合_ErrMissingEnv が返ること":     {missingKey: EnvKeyTextractSNSTopicARN, requirement: RequireTextractSNSTopicARN},
		"異常系_必須の Textract ロール ARN が欠落した場合_ErrMissingEnv が返ること": {missingKey: EnvKeyTextractRoleARN, requirement: RequireTextractRoleARN},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := fullEnv()
			env[tt.missingKey] = ""
			setEnv(t, env)

			if _, err := Load(tt.requirement); !errors.Is(err, ErrMissingEnv) {
				t.Fatalf("Load() error = %v, want %v", err, ErrMissingEnv)
			} else if !strings.Contains(err.Error(), tt.missingKey) {
				t.Errorf("error %q does not mention %q", err, tt.missingKey)
			}
		})
	}
}

// 関数ごとに必要な設定値が異なるため、要求していない項目の欠落は許容する
func TestLoadIgnoresUnrequestedValues(t *testing.T) {
	env := fullEnv()
	env[EnvKeyBedrockModelID] = ""
	env[EnvKeyDocumentsBucket] = ""
	setEnv(t, env)

	cfg, err := Load(RequireJobsTable)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.JobsTable != "dev-folio-jobs" {
		t.Errorf("JobsTable = %q", cfg.JobsTable)
	}
	if cfg.BedrockModelID != "" {
		t.Errorf("BedrockModelID = %q, want empty", cfg.BedrockModelID)
	}
}

// 起動時に不足をまとめて把握できるよう、欠落は 1 件目で打ち切らない
func TestLoadReportsAllProblems(t *testing.T) {
	setEnv(t, map[string]string{})

	_, err := Load(RequireDocumentsBucket, RequireJobsTable, RequireBedrockModelID)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, key := range []string{
		EnvKeyEnvironment,
		EnvKeyDocumentsBucket,
		EnvKeyJobsTable,
		EnvKeyBedrockModelID,
	} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %q", err, key)
		}
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	env := fullEnv()
	env[EnvKeyJobsTable] = "  dev-folio-jobs  "
	setEnv(t, env)

	cfg, err := Load(RequireJobsTable)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.JobsTable != "dev-folio-jobs" {
		t.Errorf("JobsTable = %q, want %q", cfg.JobsTable, "dev-folio-jobs")
	}
}

func TestLoadUnknownRequirement(t *testing.T) {
	setEnv(t, fullEnv())

	if _, err := Load(Requirement("FOLIO_NOT_DEFINED")); !errors.Is(err, ErrUnknownRequirement) {
		t.Fatalf("Load() error = %v, want %v", err, ErrUnknownRequirement)
	}
}

func TestLoadReturnsZeroConfigOnError(t *testing.T) {
	setEnv(t, map[string]string{})

	cfg, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if cfg != (Config{}) {
		t.Errorf("Config = %+v, want zero value", cfg)
	}
}

// isolateAWSEnv は共有設定ファイルとプロファイルの影響を排除する
//
// 実行環境のプロファイルが別リージョンを指していても結果が変わらないことを保証する
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "absent")
	t.Setenv("AWS_CONFIG_FILE", missing)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", missing)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "dummy")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "dummy")
}

// SDK の解決順ではなく Config.Region が採用されることを確かめる
func TestConfigLoadAWSUsesConfigRegion(t *testing.T) {
	tests := map[string]struct {
		awsRegion string
	}{
		"正常系_AWS_REGION が未設定の場合_Config.Region が採用されること":    {},
		"正常系_AWS_REGION が別リージョンの場合_Config.Region が採用されること": {awsRegion: "ap-northeast-1"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := fullEnv()
			env[EnvKeyRegion] = tt.awsRegion
			setEnv(t, env)
			isolateAWSEnv(t)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}

			awsCfg, err := cfg.LoadAWS(context.Background())
			if err != nil {
				t.Fatalf("LoadAWS() error = %v, want nil", err)
			}
			if awsCfg.Region != cfg.Region {
				t.Errorf("aws.Config.Region = %q, want %q", awsCfg.Region, cfg.Region)
			}
		})
	}
}

// AWS_REGION が無いローカル実行でも us-east-1 に固定されることを確かめる
func TestConfigLoadAWSFallsBackToDefaultRegion(t *testing.T) {
	env := fullEnv()
	env[EnvKeyRegion] = ""
	setEnv(t, env)
	isolateAWSEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	awsCfg, err := cfg.LoadAWS(context.Background())
	if err != nil {
		t.Fatalf("LoadAWS() error = %v, want nil", err)
	}
	if awsCfg.Region != DefaultRegion {
		t.Errorf("aws.Config.Region = %q, want %q", awsCfg.Region, DefaultRegion)
	}
}

// プロファイルが別リージョンを指すローカル実行で乖離が起きないことを確かめる
func TestConfigLoadAWSOverridesProfileRegion(t *testing.T) {
	env := fullEnv()
	env[EnvKeyRegion] = ""
	setEnv(t, env)
	isolateAWSEnv(t)

	sharedConfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(sharedConfig, []byte("[default]\nregion = ap-northeast-1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", sharedConfig)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	awsCfg, err := cfg.LoadAWS(context.Background())
	if err != nil {
		t.Fatalf("LoadAWS() error = %v, want nil", err)
	}
	if awsCfg.Region != DefaultRegion {
		t.Errorf("aws.Config.Region = %q, want %q (profile region must not win)", awsCfg.Region, DefaultRegion)
	}
}

func TestEnvValid(t *testing.T) {
	for _, e := range []Env{EnvDev, EnvStg, EnvPrd} {
		if !e.Valid() {
			t.Errorf("Env(%q).Valid() = false, want true", e)
		}
	}
	for _, e := range []Env{"", "prod", "DEV", "local"} {
		if e.Valid() {
			t.Errorf("Env(%q).Valid() = true, want false", e)
		}
	}
}
