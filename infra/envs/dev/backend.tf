# state バケット prd-folio-tfstate は Terraform の管理外 (ユーザーが事前に作成済み)
# 全環境で 1 バケットを共有し、key を envs/{env}/ で分ける
#
# region は state バケットの所在 (ap-northeast-1) で、リソースを置く provider の region (us-east-1) とは独立
# backend ブロックでは変数を参照できないためリテラルで書く
#
# ロックは S3 ネイティブ (state と同じバケットに {key}.tflock を条件付き書き込み)
# DynamoDB のロックテーブル (dynamodb_table) は Terraform 1.11 で非推奨になったため使わない
terraform {
  backend "s3" {
    bucket       = "prd-folio-tfstate"
    key          = "envs/dev/terraform.tfstate"
    region       = "ap-northeast-1"
    encrypt      = true
    use_lockfile = true
  }
}
