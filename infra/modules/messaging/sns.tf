# 暗号化しない (kms_master_key_id を設定しない)
# 通知の中身は Textract の JobId と S3 の所在で機密ではなく、CMK を使うと鍵の管理と Textract の publish ロールへの kms 権限の配布が増えるため
#trivy:ignore:AVD-AWS-0095
resource "aws_sns_topic" "textract_completion" {
  name = "${local.name_prefix}-textract-completion"
}

# Textract は NotificationChannel で渡された publish ロールを assume してトピックへ発行するため、許可する principal はそのロールにする (textract.amazonaws.com の service principal は使わない)
# 同一アカウントの publish ロールは iam 側の identity ポリシーだけでも Publish できるが、トピック側でも発行元をこのロールに限ることを明示する
# トピックポリシーを置くと既定のポリシーが置き換わるので、必要なステートメントだけにする
resource "aws_sns_topic_policy" "textract_completion" {
  arn    = aws_sns_topic.textract_completion.arn
  policy = data.aws_iam_policy_document.textract_completion.json
}

data "aws_iam_policy_document" "textract_completion" {
  statement {
    sid     = "AllowTextractPublishRole"
    actions = ["sns:Publish"]

    principals {
      type        = "AWS"
      identifiers = [var.textract_publish_role_arn]
    }

    resources = [aws_sns_topic.textract_completion.arn]
  }
}

# 購読先は起動と同じ textract-parser Lambda (SNS イベントを判別して GetDocumentAnalysis と SendTaskSuccess / SendTaskFailure を行う)
# 同一アカウントの Lambda は購読の確認が要らない
# SNS → Lambda 側に DLQ は置かない
# Lambda の非同期呼び出しの既定の再試行 (2 回) に任せ、それでも届かなければ Step Functions 側の TimeoutSeconds で経路 A を諦めて経路 B の結果で finalizer を通す
resource "aws_sns_topic_subscription" "textract_completion" {
  topic_arn = aws_sns_topic.textract_completion.arn
  protocol  = "lambda"
  endpoint  = var.textract_parser_function_arn
}

# SNS が textract-parser を呼ぶためのリソースポリシー (source_arn でこのトピックからの呼び出しに限る)
resource "aws_lambda_permission" "textract_completion" {
  statement_id  = "AllowInvokeFromTextractCompletionTopic"
  action        = "lambda:InvokeFunction"
  function_name = var.textract_parser_function_arn
  principal     = "sns.amazonaws.com"
  source_arn    = aws_sns_topic.textract_completion.arn
}
