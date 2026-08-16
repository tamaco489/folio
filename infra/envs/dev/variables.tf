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
