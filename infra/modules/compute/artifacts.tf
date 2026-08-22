# オブジェクトが無いと plan の段階で失敗する (先に backend の just upload / just upload-layer で置く)
# 関数は key だけを使い、version_id は参照しない (コードの差し替えは just upload が行う)
# Layer は version_id を s3_object_version に渡し、同じキーへの上書きを新しい Layer version として検出させる
data "aws_s3_object" "function" {
  for_each = local.functions

  bucket = var.artifacts_bucket_name
  key    = "${local.lambda_key_prefix}${each.value.name}.zip"
}

data "aws_s3_object" "layer" {
  bucket = var.artifacts_bucket_name
  key    = local.layer_key
}
