resource "aws_iam_role" "lambda_finalize" {
  name               = "${local.name_prefix}-lambda-finalize-role"
  description        = "Execution role of the finalizer Lambda (reads work/, writes outputs/, updates the job status)."
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

  # 欠けた中間結果 (経路の失敗や skip) を s3.ErrNotFound で読み飛ばすために要る
  # ListBucket が無いと S3 は存在しないキーの GetObject を 404 ではなく 403 で返し、NotFound 判定に入らない
  statement {
    sid       = "ListWork"
    actions   = ["s3:ListBucket"]
    resources = [var.documents_bucket_arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["work/*"]
    }
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
