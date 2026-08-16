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

# ------------------------------------------------------------------------------
# 信頼ポリシー
# ------------------------------------------------------------------------------

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

# ------------------------------------------------------------------------------
# Step Functions の実行ロール
# ------------------------------------------------------------------------------

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

# ------------------------------------------------------------------------------
# Lambda の実行ロール (関数ごとに分け、パーサー 2 つだけ共用する)
# ------------------------------------------------------------------------------

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

resource "aws_iam_role" "lambda_preprocess" {
  name               = "${local.name_prefix}-lambda-preprocess-role"
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

resource "aws_iam_role" "lambda_parser" {
  name               = "${local.name_prefix}-lambda-parser-role"
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

# ------------------------------------------------------------------------------
# Textract が完了通知を SNS へ発行するために引き受けるロール
# ------------------------------------------------------------------------------

# Lambda に付けるロールではなく、StartDocumentAnalysis の NotificationChannel.RoleArn として Textract に渡す
resource "aws_iam_role" "textract_publish" {
  name               = "${local.name_prefix}-textract-publish-role"
  assume_role_policy = data.aws_iam_policy_document.textract_publish_assume.json
}

data "aws_iam_policy_document" "textract_publish" {
  statement {
    sid       = "PublishCompletion"
    actions   = ["sns:Publish"]
    resources = [var.textract_completion_topic_arn]
  }
}

resource "aws_iam_role_policy" "textract_publish" {
  name   = "${local.name_prefix}-textract-publish-policy"
  role   = aws_iam_role.textract_publish.name
  policy = data.aws_iam_policy_document.textract_publish.json
}
