# s3_object_version が変わると新しい Layer version が発行され、関数側の layers の ARN も追従して更新される
# source_code_hash は使わない (差分の検出はバージョニングに寄せ、CI はアップロードまで、反映は Terraform という分担にする)
resource "aws_lambda_layer_version" "pdf_processor" {
  layer_name        = "${local.name_prefix}-pdf-processor"
  s3_bucket         = var.artifacts_bucket_name
  s3_key            = data.aws_s3_object.layer.key
  s3_object_version = data.aws_s3_object.layer.version_id

  compatible_runtimes      = ["provided.al2023"]
  compatible_architectures = ["arm64"]
}
