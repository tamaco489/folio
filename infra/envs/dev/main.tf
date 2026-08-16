# 環境ディレクトリは値 (env, account_id, region) を渡すだけで、リソース名はモジュール内で "${var.env}-folio-${local.name}" として組み立てる
# ここに name_prefix のような local は置かない
#
# モジュール間の受け渡しは outputs 経由で行う
# iam ↔ messaging、iam ↔ compute、messaging ↔ pipeline はモジュール同士が互いの出力を参照するが、リソース単位の依存は循環しない
# (例: SNS トピック → publish ロールのポリシー、publish ロール → トピックポリシー)
# module ブロックに depends_on を書くとモジュール全体の依存になって循環するため書かない
#
# storage 以外は compute が artifacts バケットの zip を data で読むため、初回は storage だけを apply して zip を置いてから全体を apply する (README を参照)

module "storage" {
  source     = "../../modules/storage"
  env        = var.env
  account_id = var.account_id
}

module "iam" {
  source                        = "../../modules/iam"
  env                           = var.env
  region                        = var.region
  account_id                    = var.account_id
  documents_bucket_arn          = module.storage.documents_bucket_arn
  jobs_table_arn                = module.storage.jobs_table_arn
  textract_completion_topic_arn = module.messaging.textract_completion_topic_arn
  lambda_function_arns          = module.compute.function_arns
  lambda_log_group_arns         = module.compute.log_group_arns
}

module "compute" {
  source                        = "../../modules/compute"
  env                           = var.env
  documents_bucket_name         = module.storage.documents_bucket_name
  jobs_table_name               = module.storage.jobs_table_name
  artifacts_bucket_name         = module.storage.artifacts_bucket_name
  lambda_validate_role_arn      = module.iam.lambda_validate_role_arn
  lambda_preprocess_role_arn    = module.iam.lambda_preprocess_role_arn
  lambda_parser_role_arn        = module.iam.lambda_parser_role_arn
  lambda_finalize_role_arn      = module.iam.lambda_finalize_role_arn
  textract_completion_topic_arn = module.messaging.textract_completion_topic_arn
  textract_publish_role_arn     = module.iam.textract_publish_role_arn
  bedrock_model_id              = var.bedrock_model_id
  crossref_mailto               = var.crossref_mailto
}

module "pipeline" {
  source                = "../../modules/pipeline"
  env                   = var.env
  statemachine_role_arn = module.iam.statemachine_role_arn
  function_arns         = module.compute.function_arns
}

module "messaging" {
  source                       = "../../modules/messaging"
  env                          = var.env
  documents_bucket_name        = module.storage.documents_bucket_name
  state_machine_arn            = module.pipeline.state_machine_arn
  textract_publish_role_arn    = module.iam.textract_publish_role_arn
  textract_parser_function_arn = module.compute.function_arns["textract-parser"]
}

# TF_VAR_account_id と認証情報のアカウントが食い違ったまま apply すると別アカウントの ID を名前に含むバケットが作られるため、plan の段階で止める
# 属性はどこからも参照せず postcondition のためだけに宣言しているので、tflint の未使用検査は抑止する
# tflint-ignore: terraform_unused_declarations
data "aws_caller_identity" "current" {
  lifecycle {
    postcondition {
      condition     = self.account_id == var.account_id
      error_message = "TF_VAR_account_id (${var.account_id}) does not match the account of the current AWS credentials (${self.account_id})."
    }
  }
}
