resource "aws_iam_role" "lambda_finalize" {
  name               = "${local.name_prefix}-lambda-finalize-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

# finalizer は uploads/ の原本のメタデータと work/ の中間結果を読み、outputs/ に成果物を書いて UpdateItem で状態を更新する
# Crossref は外部 HTTPS で AWS の権限は要らない
data "aws_iam_policy_document" "lambda_finalize" {
  statement {
    sid       = "ReadUploadsAndWork"
    actions   = ["s3:GetObject"]
    resources = [local.uploads_objects, local.work_objects]
  }

  statement {
    sid       = "WriteOutputs"
    actions   = ["s3:PutObject"]
    resources = [local.outputs_objects]
  }

  statement {
    sid       = "UpdateJobStatus"
    actions   = ["dynamodb:UpdateItem"]
    resources = [var.jobs_table_arn]
  }

  statement {
    sid       = "WriteLogs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = [local.log_stream_arns["finalizer"]]
  }
}

resource "aws_iam_role_policy" "lambda_finalize" {
  name   = "${local.name_prefix}-lambda-finalize-policy"
  role   = aws_iam_role.lambda_finalize.name
  policy = data.aws_iam_policy_document.lambda_finalize.json
}
