resource "aws_iam_role" "lambda_parser" {
  name               = "${local.name_prefix}-lambda-parser-role"
  description        = "Execution role shared by textract-parser and bedrock-parser (S3 work/, Textract, Bedrock, Step Functions task token)."
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

# textract-parser と bedrock-parser で共用する
# S3 の読み書きと Bedrock の呼び出しは両者で同じで、差分は Textract の起動と Step Functions への応答だけのため
# Textract は StartDocumentAnalysis を呼んだ側の権限で uploads/ の原本を読むので、uploads/ の GetObject はこのロールに要る
data "aws_iam_policy_document" "lambda_parser" {
  statement {
    sid       = "ReadUploadsAndWork"
    actions   = ["s3:GetObject"]
    resources = [local.uploads_objects, local.work_objects]
  }

  statement {
    sid       = "WriteWork"
    actions   = ["s3:PutObject"]
    resources = [local.work_objects]
  }

  # Textract の API はリソース単位の制御を持たないため * にする
  statement {
    sid       = "AnalyzeDocument"
    actions   = ["textract:StartDocumentAnalysis", "textract:GetDocumentAnalysis"]
    resources = ["*"]
  }

  # StartDocumentAnalysis の NotificationChannel に publish ロールを渡すために要る
  # 渡し先を Textract に限り、他サービスへ同じロールを渡せないようにする
  statement {
    sid       = "PassPublishRoleToTextract"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.textract_publish.arn]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["textract.amazonaws.com"]
    }
  }

  # Converse API は InvokeModel の権限で認可される
  statement {
    sid       = "InvokeBedrockModel"
    actions   = ["bedrock:InvokeModel"]
    resources = local.bedrock_resources
  }

  # SendTaskSuccess / SendTaskFailure はタスクトークンで認可され、リソース単位の制御を持たないため * にする
  statement {
    sid       = "RespondToTaskToken"
    actions   = ["states:SendTaskSuccess", "states:SendTaskFailure"]
    resources = ["*"]
  }

  statement {
    sid     = "WriteLogs"
    actions = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = [
      local.log_stream_arns["textract-parser"],
      local.log_stream_arns["bedrock-parser"],
    ]
  }
}

resource "aws_iam_role_policy" "lambda_parser" {
  name   = "${local.name_prefix}-lambda-parser-policy"
  role   = aws_iam_role.lambda_parser.name
  policy = data.aws_iam_policy_document.lambda_parser.json
}
