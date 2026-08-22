# 配布は S3 + zip (provided.al2023 / arm64、zip の中身は bootstrap 1 ファイル)
# コードの差し替えは backend の just upload (aws lambda update-function-code) が行い、Terraform は初回作成と設定だけを担う
# s3_object_version と source_code_hash は書かない (書くと upload のたびに apply が要る。どちらも API から読み戻されないため外部の差し替えで drift は出ない)
# publish は false (バージョンとエイリアスを使わず、Step Functions は $LATEST を直接呼ぶ)
resource "aws_lambda_function" "pipeline" {
  for_each = local.functions

  function_name = local.function_names[each.key]
  description   = each.value.description
  role          = each.value.role_arn

  package_type = "Zip"
  s3_bucket    = var.artifacts_bucket_name
  s3_key       = data.aws_s3_object.function[each.key].key

  runtime       = "provided.al2023"
  architectures = ["arm64"]
  handler       = "bootstrap"
  publish       = false

  memory_size = each.value.memory_size
  timeout     = each.value.timeout
  layers      = each.value.layers

  ephemeral_storage {
    size = each.value.ephemeral_storage
  }

  environment {
    variables = merge({ FOLIO_ENV = var.env }, each.value.environment)
  }

  # backend は Go 標準の log / slog のテキスト出力で、ログレベルによる絞り込みも使わないため Text にする
  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.pipeline[each.key].name
  }
}
