#!/usr/bin/env bash
#
# PDF を documents バケットに置いてパイプラインを起動する
#
# 使い方
#   scripts/submit.sh <pdf> [env]
#
# env の既定は dev。バケットは {env}-folio-documents-{アカウント ID} で、Terraform (infra/modules/storage) が作る
# アカウント ID は環境変数 TF_VAR_account_id から取り、未設定なら aws sts get-caller-identity で調べる
# AWS の認証情報は AWS_PROFILE などで用意する
#
# jobId は PDF の SHA-256 (validator が中身のハッシュとキーの jobId を照合し、違うと HASH_MISMATCH で Rejected になる)
# キーは uploads/{jobId}/original.pdf で、EventBridge がこのキーの Object Created でステートマシンを起動する
# 同じ PDF を再投入すると validator の冪等性チェックで Skipped になる。再実行するときは先に scripts/cleanup.sh で消す
#
# Textract と Bedrock の課金が発生するため、実行はユーザーが行う
#
# カレントディレクトリはどこでもよい (pdf の相対パスは呼び出し元から解決し、その後スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

pdf="${1:-}"
env="${2:-dev}"

if [ -z "$pdf" ]; then
    echo "使い方: scripts/submit.sh <pdf> [env]" >&2
    exit 1
fi
if [ ! -f "$pdf" ]; then
    echo "PDF がありません: $pdf" >&2
    exit 1
fi
pdf="$(cd -- "$(dirname -- "$pdf")" && pwd)/$(basename -- "$pdf")"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

if [ "$(head -c 5 "$pdf")" != "%PDF-" ]; then
    echo "PDF の署名 (%PDF-) がありません: $pdf" >&2
    exit 1
fi

account_id="${TF_VAR_account_id:-}"
if [ -z "$account_id" ]; then
    account_id=$(aws sts get-caller-identity --query Account --output text)
fi
if ! [[ "$account_id" =~ ^[0-9]{12}$ ]]; then
    echo "アカウント ID が 12 桁の数字ではありません: $account_id" >&2
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    job_id=$(sha256sum "$pdf" | cut -d' ' -f1)
else
    job_id=$(shasum -a 256 "$pdf" | cut -d' ' -f1)
fi

bucket="${env}-folio-documents-${account_id}"
key="uploads/${job_id}/original.pdf"

aws s3 cp "$pdf" "s3://${bucket}/${key}"

echo "jobId: ${job_id}"
echo "次: just status ${job_id} ${env}"
