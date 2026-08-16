locals {
  name_prefix = "${var.env}-folio"

  # S3 キーの第 1 階層ごとのオブジェクト ARN (backend/internal/awsx/s3/keys.go と対応)
  # ListBucket はどの Lambda も呼ばないため、バケット ARN そのものへの許可は出さない
  uploads_objects = "${var.documents_bucket_arn}/uploads/*"
  work_objects    = "${var.documents_bucket_arn}/work/*"
  outputs_objects = "${var.documents_bucket_arn}/outputs/*"

  # ロググループの ARN は末尾に :* を付けてログストリームまで含める (CreateLogStream / PutLogEvents の対象はログストリーム)
  # ロググループ自体は compute が Terraform で作るため CreateLogGroup は付けない
  log_stream_arns = { for key, arn in var.lambda_log_group_arns : key => "${arn}:*" }

  # Bedrock はモデル単位まで絞らない
  # Phase 1 はモデルを差し替えて抽出精度を比較する段階で、差し替えのたびにポリシーを直すことになるため
  # クロスリージョン推論プロファイル (us.anthropic.claude-...) 経由の呼び出しは、プロファイル本体と転送先リージョンの基盤モデルの双方に InvokeModel の許可が要る
  # 転送先リージョンは AWS 側で決まるためリージョンをワイルドカードにする (基盤モデルの ARN はアカウント ID を持たない)
  bedrock_resources = [
    "arn:aws:bedrock:${var.region}:${var.account_id}:inference-profile/*",
    "arn:aws:bedrock:*::foundation-model/*",
  ]
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

# Step Functions と Textract はサービスがロールを引き受けるため、他アカウントのリソース経由で引き受けられないよう自アカウント発に限る (confused deputy 対策)
data "aws_iam_policy_document" "statemachine_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["states.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [var.account_id]
    }

    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:aws:states:${var.region}:${var.account_id}:stateMachine:*"]
    }
  }
}

# Textract の非同期ジョブは ARN を事前に知れないため、SourceArn はリソース部分をワイルドカードにする (公式の例と同じ形)
# DOC: https://docs.aws.amazon.com/textract/latest/dg/cross-service-confused-deputy-prevention.html
data "aws_iam_policy_document" "textract_publish_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["textract.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [var.account_id]
    }

    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:aws:textract:${var.region}:${var.account_id}:*"]
    }
  }
}
