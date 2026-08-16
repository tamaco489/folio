# S3 側は EventBridge 通知をバケット全体で有効にしているだけなので、絞り込みはこのルールのイベントパターンで行う (storage の aws_s3_bucket_notification を参照)
# EventBridge のイベントパターンは同じフィールドの配列に並べた値を OR で評価し、同じキーを 2 回書くと後の方だけが評価されるため、prefix と suffix を並べても AND にならない
# 1 つの文字列で前方と後方の両方を指定できるのは wildcard だけなので、wildcard で uploads/ プレフィックスと .pdf サフィックスの両方を表す
# uploads/*/original.pdf は jobId に / が含まれないため、* が / をまたぐかによらず uploads/{jobId}/original.pdf に一致する
# 既存キーの上書きも Object Created として届くが、そのまま起動させる (同じ内容なら jobId (SHA-256) が同じで validator の冪等性チェックが SKIPPED にする)
resource "aws_cloudwatch_event_rule" "upload_trigger" {
  name = "${local.name_prefix}-upload-trigger"

  event_pattern = jsonencode({
    source      = ["aws.s3"]
    detail-type = ["Object Created"]
    detail = {
      bucket = {
        name = [var.documents_bucket_name]
      }
      object = {
        key = [{ wildcard = local.upload_key_wildcard }]
      }
    }
  })
}

# input transformer は付けず S3 イベント全体を実行入力にする (ASL が $.detail.bucket.name と $.detail.object.key を読む)
resource "aws_cloudwatch_event_target" "upload_trigger" {
  rule     = aws_cloudwatch_event_rule.upload_trigger.name
  arn      = var.state_machine_arn
  role_arn = aws_iam_role.eventbridge_invoke.arn

  dead_letter_config {
    arn = aws_sqs_queue.upload_trigger_dlq.arn
  }
}

# ターゲットの起動に失敗したイベントを DLQ に残す
# 起動失敗の取りこぼしを検知する手段にする (無限ループのような事故も、キューにイベントが溜まることで早期に気付ける)
# 暗号化は SQS 管理の SSE で足りる (中身は S3 イベントで、CMK を使う理由がない)
resource "aws_sqs_queue" "upload_trigger_dlq" {
  name                      = "${local.name_prefix}-upload-trigger-dlq"
  message_retention_seconds = local.dlq_message_retention_seconds
  sqs_managed_sse_enabled   = true
}

# EventBridge が DLQ に書き込めるようにする
# SourceArn をルールに限ることで、他のルールや他アカウントからの書き込みを防ぐ
resource "aws_sqs_queue_policy" "upload_trigger_dlq" {
  queue_url = aws_sqs_queue.upload_trigger_dlq.id
  policy    = data.aws_iam_policy_document.upload_trigger_dlq.json
}

data "aws_iam_policy_document" "upload_trigger_dlq" {
  statement {
    sid     = "AllowEventBridgeDeadLetter"
    actions = ["sqs:SendMessage"]

    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }

    resources = [aws_sqs_queue.upload_trigger_dlq.arn]

    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_cloudwatch_event_rule.upload_trigger.arn]
    }
  }
}

# EventBridge がステートマシンを起動するためのロール
# Step Functions はリソースポリシーを持たないため、ターゲット側に IAM ロールが要る
resource "aws_iam_role" "eventbridge_invoke" {
  name               = "${local.name_prefix}-eventbridge-invoke-role"
  assume_role_policy = data.aws_iam_policy_document.eventbridge_assume.json
}

# assume できる呼び出し元をこのルールに限る (confused deputy 対策)
# ルール ARN にアカウント ID が含まれるため aws:SourceAccount は重ねない
data "aws_iam_policy_document" "eventbridge_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }

    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_cloudwatch_event_rule.upload_trigger.arn]
    }
  }
}

data "aws_iam_policy_document" "eventbridge_invoke" {
  statement {
    sid       = "StartPipeline"
    actions   = ["states:StartExecution"]
    resources = [var.state_machine_arn]
  }
}

resource "aws_iam_role_policy" "eventbridge_invoke" {
  name   = "${local.name_prefix}-eventbridge-invoke-policy"
  role   = aws_iam_role.eventbridge_invoke.name
  policy = data.aws_iam_policy_document.eventbridge_invoke.json
}
