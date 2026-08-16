# state バケットは環境ごとに {env}-folio-tfstate を用意する (Terraform の管理外。ユーザーが事前に作成する)
# dev は dev-folio-tfstate、key は envs/{env}/terraform.tfstate
#
# region は state バケットの所在 (ap-northeast-1) で、リソースを置く provider の region (us-east-1) とは独立 backend ブロックでは変数を参照できないためリテラルで書く
#
# ロックは S3 ネイティブ (state と同じバケットに {key}.tflock を条件付き書き込み)
# DynamoDB のロックテーブル (dynamodb_table) は Terraform 1.11 で非推奨になったため使わない
terraform {
  backend "s3" {
    bucket       = "dev-folio-tfstate"
    key          = "envs/dev/terraform.tfstate"
    region       = "ap-northeast-1"
    encrypt      = true
    use_lockfile = true
  }
}
