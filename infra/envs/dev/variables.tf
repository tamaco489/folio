# default を置かず terraform.tfvars で明示する (環境ディレクトリを複製して stg / prd を作るとき、値の指定漏れが暗黙に dev へ落ちるのを防ぐため)
variable "env" {
  description = "Environment identifier used as the resource name prefix (dev, stg, prd)."
  type        = string

  validation {
    condition     = contains(["dev", "stg", "prd"], var.env)
    error_message = "env must be one of: dev, stg, prd."
  }
}

# アカウント ID はリポジトリに置かず TF_VAR_account_id で渡す
# バケット名など公開情報にも入るため sensitive にはしない (plan の可読性を優先)
variable "account_id" {
  description = "AWS account ID (12 digits) that owns this environment; passed via TF_VAR_account_id."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.account_id))
    error_message = "account_id must be a 12-digit AWS account ID."
  }
}

# Phase 1 は us-east-1 のみ (理由: Bedrock のモデル可用性と arXiv バルクデータの所在)
# S3 はリージョンを後から変えられないため、変更は作り直しを伴う
variable "region" {
  description = "AWS region for all resources in this environment."
  type        = string
  default     = "us-east-1"
}

# 構造化に使う Bedrock のモデル ID (textract-parser と bedrock-parser の FOLIO_BEDROCK_MODEL_ID)
# Phase 1 はモデルを差し替えて比較するため tfvars で切り替える。IAM はモデル単位に絞っていないのでポリシーの変更は要らない
variable "bedrock_model_id" {
  description = "Bedrock model ID (or cross-region inference profile ID) used by both parsers."
  type        = string
}

# Crossref の polite pool 用の連絡先 (finalizer の FOLIO_CROSSREF_MAILTO)
# メールアドレスをリポジトリに置かないよう tfvars には書かず TF_VAR_crossref_mailto で渡す。空なら環境変数を設定しない
variable "crossref_mailto" {
  description = "Contact address sent to Crossref for the polite pool; empty disables it."
  type        = string
  default     = ""
}
