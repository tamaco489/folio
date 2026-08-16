# 環境ディレクトリは値 (env, account_id, region) を渡すだけで、リソース名はモジュール内で
# "${var.env}-folio-${local.name}" として組み立てる。ここに name_prefix のような local は置かない
#
# モジュール呼び出しは #31 で足す。想定する形:
#
#   module "storage" {
#     source     = "../../modules/storage"
#     env        = var.env
#     account_id = var.account_id
#     region     = var.region
#   }
#
#   (messaging, compute, pipeline, iam も同様。モジュール間の受け渡しは outputs 経由)

# TF_VAR_account_id と認証情報のアカウントが食い違ったまま apply すると
# 別アカウントの ID を名前に含むバケットが作られるため、plan の段階で止める
data "aws_caller_identity" "current" {
  lifecycle {
    postcondition {
      condition     = self.account_id == var.account_id
      error_message = "TF_VAR_account_id (${var.account_id}) does not match the account of the current AWS credentials (${self.account_id})."
    }
  }
}
