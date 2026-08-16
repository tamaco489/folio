# オブジェクトが無いと plan の段階で失敗する (先に backend の just package と aws s3 cp で置く)
# version_id を関数と Layer の s3_object_version に渡し、同じキーへの上書きを更新として検出させる
data "aws_s3_object" "function" {
  for_each = local.functions

  bucket = var.artifacts_bucket_name
  key    = "${local.lambda_key_prefix}${each.value.name}.zip"
}

data "aws_s3_object" "layer" {
  bucket = var.artifacts_bucket_name
  key    = local.layer_key
}
