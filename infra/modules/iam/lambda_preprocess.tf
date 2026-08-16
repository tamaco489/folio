resource "aws_iam_role" "lambda_preprocess" {
  name               = "${local.name_prefix}-lambda-preprocess-role"
  description        = "Execution role of the preprocessor Lambda (reads uploads/, writes page images and the text layer to work/)."
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

# preprocessor は uploads/ の原本を読み、ページ画像とテキストレイヤーを work/ に書く
data "aws_iam_policy_document" "lambda_preprocess" {
  statement {
    sid       = "ReadUploads"
    actions   = ["s3:GetObject"]
    resources = [local.uploads_objects]
  }

  statement {
    sid       = "WriteWork"
    actions   = ["s3:PutObject"]
    resources = [local.work_objects]
  }

  statement {
    sid       = "WriteLogs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = [local.log_stream_arns["preprocessor"]]
  }
}

resource "aws_iam_role_policy" "lambda_preprocess" {
  name   = "${local.name_prefix}-lambda-preprocess-policy"
  role   = aws_iam_role.lambda_preprocess.name
  policy = data.aws_iam_policy_document.lambda_preprocess.json
}
