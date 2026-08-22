#!/usr/bin/env bash
#
# 再投入のためにジョブの痕跡を消す
#
# 使い方
#   scripts/cleanup.sh <jobId> [env]
#
# DynamoDB の jobs レコードと、documents バケットの uploads/ work/ outputs/ の {jobId}/ 配下を消す
# validator の冪等性チェック (同じ PDF は Skipped) を通すために要る
# Step Functions の実行履歴は消さない (消せないため)
#
# env の既定は dev。リージョンは AWS_REGION から取り、未設定なら us-east-1 (infra/envs/*/variables.tf の既定)
# アカウント ID は環境変数 TF_VAR_account_id から取り、未設定なら aws sts get-caller-identity で調べる
#
# 消す前に対象を表示して確認を求める。実行はユーザーが行う
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

job_id="${1:-}"
env="${2:-dev}"
region="${AWS_REGION:-us-east-1}"

if [ -z "$job_id" ]; then
    echo "使い方: scripts/cleanup.sh <jobId> [env]" >&2
    exit 1
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

account_id="${TF_VAR_account_id:-}"
if [ -z "$account_id" ]; then
    account_id=$(aws sts get-caller-identity --query Account --output text)
fi
if ! [[ "$account_id" =~ ^[0-9]{12}$ ]]; then
    echo "アカウント ID が 12 桁の数字ではありません: $account_id" >&2
    exit 1
fi

prefix="${env}-folio"
bucket="${prefix}-documents-${account_id}"
table="${prefix}-jobs"

echo "次を消します"
echo "  DynamoDB: ${table} の jobId=${job_id}"
for p in uploads work outputs; do
    echo "  S3: s3://${bucket}/${p}/${job_id}/"
done
read -r -p "続けますか? [y/N] " answer
if [ "$answer" != "y" ]; then
    echo "中止しました"
    exit 1
fi

aws dynamodb delete-item --region "$region" --table-name "$table" --key "{\"jobId\":{\"S\":\"${job_id}\"}}"
for p in uploads work outputs; do
    aws s3 rm "s3://${bucket}/${p}/${job_id}/" --recursive
done
echo "消しました: ${job_id}"
