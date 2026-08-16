resource "aws_iam_role" "lambda_validate" {
  name               = "${local.name_prefix}-lambda-validate-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

# validator は uploads/ の原本を読んで SHA-256 とページ数を調べる (HeadObject は GetObject で認可される)
# ジョブは条件付き PutItem で登録し、弾いたときだけ UpdateItem で FAILED にする (GetItem / Query は呼ばない)
data "aws_iam_policy_document" "lambda_validate" {
  statement {
    sid       = "ReadUploads"
    actions   = ["s3:GetObject"]
    resources = [local.uploads_objects]
  }

  statement {
    sid       = "RegisterAndFailJob"
    actions   = ["dynamodb:PutItem", "dynamodb:UpdateItem"]
    resources = [var.jobs_table_arn]
  }

  statement {
    sid       = "WriteLogs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = [local.log_stream_arns["validator"]]
  }
}

resource "aws_iam_role_policy" "lambda_validate" {
  name   = "${local.name_prefix}-lambda-validate-policy"
  role   = aws_iam_role.lambda_validate.name
  policy = data.aws_iam_policy_document.lambda_validate.json
}
