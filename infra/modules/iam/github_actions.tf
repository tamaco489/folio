# GitHub Actions (.github/workflows/cd-backend.yml) が OIDC で引き受けるロール
# アクセスキーをリポジトリに置かず、ワークフローが発行する JWT をこのプロバイダ経由で STS に交換する

# OIDC プロバイダの ARN は URL で決まる (arn:aws:iam::{account_id}:oidc-provider/token.actions.githubusercontent.com) ため、アカウントに 1 つしか作れない
# 同じアカウントに stg / prd を足すと iam モジュールが 2 つ目を作ろうとして EntityAlreadyExists で失敗するので、その時点でプロバイダをモジュール外へ切り出す (Phase 1 は dev のみ)
# thumbprint_list は書かない
# AWS は token.actions.githubusercontent.com の証明書を自前の信頼済みルート CA で検証するため値は使われず、provider 6.x でも任意
# 一度書いた thumbprint_list は消しても provider が元の値を使い続けるため、後から足さない
resource "aws_iam_openid_connect_provider" "github_actions" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

# 引き受けをこのリポジトリの main ブランチで動くワークフローに限る (environment を使わない workflow_dispatch の sub は repo:{github_repository}:ref:refs/heads/{branch})
# 2026-07-15 以降に作られたリポジトリの sub は repo:{owner}@{owner id}/{repo}@{repo id}:... の形 (immutable subject claim) なので、github_repository にはその形で渡す
# workflow_dispatch を main 以外のブランチで実行しても sub が一致せず弾かれる
# aud は configure-aws-credentials が既定で付ける audience と同じ値
data "aws_iam_policy_document" "github_actions_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github_actions.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:ref:refs/heads/main"]
    }
  }
}

resource "aws_iam_role" "github_actions" {
  name               = "${local.name_prefix}-github-actions-role"
  assume_role_policy = data.aws_iam_policy_document.github_actions_assume.json
}

# backend/scripts/upload.sh の aws s3 cp (固定キーへの上書き) に要る権限だけを持たせる
# 単一ファイルの cp は ListBucket を呼ばないためバケット ARN そのものへの許可は出さない
# 8 MB を超える zip は multipart になるが、CreateMultipartUpload / UploadPart / CompleteMultipartUpload は PutObject で認可される
# AbortMultipartUpload は失敗時に CLI が残骸を消すために要る (残った分はバケットのライフサイクルが 7 日で消す)
# Lambda の更新 (lambda:UpdateFunctionCode) と Terraform の state には触れさせない
# upload.sh が呼ぶ sts:GetCallerIdentity は認可を要しない
data "aws_iam_policy_document" "github_actions" {
  statement {
    sid     = "UploadArtifacts"
    actions = ["s3:PutObject", "s3:AbortMultipartUpload"]
    resources = [
      "${var.artifacts_bucket_arn}/lambda/*",
      "${var.artifacts_bucket_arn}/layers/*",
    ]
  }
}

resource "aws_iam_role_policy" "github_actions" {
  name   = "${local.name_prefix}-github-actions-policy"
  role   = aws_iam_role.github_actions.name
  policy = data.aws_iam_policy_document.github_actions.json
}
