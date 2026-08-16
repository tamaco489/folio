resource "aws_iam_role" "statemachine" {
  name               = "${local.name_prefix}-statemachine-role"
  assume_role_policy = data.aws_iam_policy_document.statemachine_assume.json
}

# Textract と SNS の権限は持たせない
# 経路 A は textract-parser Lambda を waitForTaskToken で呼び、Lambda が Textract を起動して SNS の完了通知を受ける (Step Functions の直接統合は使わない)
data "aws_iam_policy_document" "statemachine" {
  # 関数のバージョン・エイリアスは作らない (Qualifier なしで呼ぶ) ため、関数 ARN そのもので足り :* は付けない
  statement {
    sid       = "InvokePipelineFunctions"
    actions   = ["lambda:InvokeFunction"]
    resources = values(var.lambda_function_arns)
  }

  # CloudWatch Logs への実行履歴の配信に要る権限 (Step Functions の公式ドキュメントが示す 10 アクションをそのまま並べる)
  # ログ配信 (log delivery) の API は CloudWatch Logs のリソースタイプを持たないため * にする
  statement {
    sid = "DeliverExecutionLogs"
    actions = [
      "logs:CreateLogDelivery",
      "logs:CreateLogStream",
      "logs:GetLogDelivery",
      "logs:UpdateLogDelivery",
      "logs:DeleteLogDelivery",
      "logs:ListLogDeliveries",
      "logs:PutLogEvents",
      "logs:PutResourcePolicy",
      "logs:DescribeResourcePolicies",
      "logs:DescribeLogGroups",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "statemachine" {
  name   = "${local.name_prefix}-statemachine-policy"
  role   = aws_iam_role.statemachine.name
  policy = data.aws_iam_policy_document.statemachine.json
}
