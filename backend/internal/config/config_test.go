package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setEnv は Load が参照する環境変数をすべて上書きする
//
// 空文字は未設定と同義に扱われるため、ホスト側の値を確実に打ち消せる
func setEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{
		EnvKeyEnvironment,
		EnvKeyRegion,
		EnvKeyDocumentsBucket,
		EnvKeyJobsTable,
		EnvKeyBedrockModelID,
	} {
		t.Setenv(key, values[key])
	}
}

func fullEnv() map[string]string {
	return map[string]string{
		EnvKeyEnvironment:     "dev",
		EnvKeyRegion:          "us-east-1",
		EnvKeyDocumentsBucket: "dev-folio-documents-000000000000",
		EnvKeyJobsTable:       "dev-folio-jobs",
		EnvKeyBedrockModelID:  "anthropic.claude-sonnet-4-20250514-v1:0",
	}
}

func TestLoadAllValuesPresent(t *testing.T) {
	setEnv(t, fullEnv())

	cfg, err := Load(RequireDocumentsBucket, RequireJobsTable, RequireBedrockModelID)
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
	tests := []struct {
		name    string
		value   string
		want    Env
		wantErr error
	}{
		{name: "dev", value: "dev", want: EnvDev},
		{name: "stg", value: "stg", want: EnvStg},
		{name: "prd", value: "prd", want: EnvPrd},
		{name: "空文字は欠落", value: "", wantErr: ErrMissingEnv},
		{name: "空白のみは欠落", value: "   ", wantErr: ErrMissingEnv},
		{name: "想定外の値", value: "prod", wantErr: ErrInvalidEnv},
		{name: "大文字は認めない", value: "DEV", wantErr: ErrInvalidEnv},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
	tests := []struct {
		name        string
		missingKey  string
		requirement Requirement
	}{
		{name: "バケット名", missingKey: EnvKeyDocumentsBucket, requirement: RequireDocumentsBucket},
		{name: "テーブル名", missingKey: EnvKeyJobsTable, requirement: RequireJobsTable},
		{name: "モデル ID", missingKey: EnvKeyBedrockModelID, requirement: RequireBedrockModelID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
	tests := []struct {
		name      string
		awsRegion string
	}{
		{name: "AWS_REGION 未設定"},
		{name: "AWS_REGION が別リージョン", awsRegion: "ap-northeast-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
