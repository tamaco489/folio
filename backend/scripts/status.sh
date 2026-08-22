#!/usr/bin/env bash
#
# パイプラインの実行状況を表示する
#
# 使い方
#   scripts/status.sh [jobId] [env]
#
# jobId を省略すると、ステートマシンの直近 5 件の実行を一覧する
# jobId を指定すると、その PDF (uploads/{jobId}/original.pdf) を入力にした最新の実行について
#   実行の状態と失敗原因、TaskFailed の原因、Preprocess の出力、DynamoDB のレコード、work/ と outputs/ の一覧
# を表示する
#
# env の既定は dev。リージョンは AWS_REGION から取り、未設定なら us-east-1 (infra/envs/*/variables.tf の既定)
# プロファイルの既定リージョン (ap-northeast-1 など) に引きずられないよう、すべてのコマンドに --region を付ける
# アカウント ID は環境変数 TF_VAR_account_id から取り、未設定なら aws sts get-caller-identity で調べる
#
# 読み取りの API しか呼ばない
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

job_id="${1:-}"
env="${2:-dev}"
region="${AWS_REGION:-us-east-1}"

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
state_machine="arn:aws:states:${region}:${account_id}:stateMachine:${prefix}-pipeline"

if [ -z "$job_id" ]; then
    echo "## 直近の実行 (${prefix}-pipeline)"
    aws stepfunctions list-executions --region "$region" --state-machine-arn "$state_machine" --max-results 5 \
        --query 'executions[].{status:status,start:startDate,stop:stopDate,name:name}' --output table
    echo "jobId を指定すると実行の詳細を表示する: scripts/status.sh <jobId> [env]"
    exit 0
fi

key="uploads/${job_id}/original.pdf"

# 実行名は EventBridge のイベント ID で jobId を含まないため、直近の実行の入力からキーで探す
execution=""
for arn in $(aws stepfunctions list-executions --region "$region" --state-machine-arn "$state_machine" --max-results 20 \
    --query 'executions[].executionArn' --output text); do
    input=$(aws stepfunctions describe-execution --region "$region" --execution-arn "$arn" --query input --output text)
    if [[ "$input" == *"$key"* ]]; then
        execution="$arn"
        break
    fi
done

echo "## 実行"
if [ -z "$execution" ]; then
    echo "直近 20 件に ${key} を入力にした実行がありません"
else
    aws stepfunctions describe-execution --region "$region" --execution-arn "$execution" \
        --query '{status:status,start:startDate,stop:stopDate,error:error,cause:cause}' --output json
    echo "## TaskFailed の原因"
    # shellcheck disable=SC2016  # バッククォートは JMESPath のリテラルで、シェルの展開ではない
    aws stepfunctions get-execution-history --region "$region" --execution-arn "$execution" \
        --query 'events[?type==`TaskFailed`].taskFailedEventDetails.cause' --output text
    echo "## Preprocess の出力"
    # shellcheck disable=SC2016  # 同上
    aws stepfunctions get-execution-history --region "$region" --execution-arn "$execution" \
        --query 'events[?type==`TaskStateExited` && stateExitedEventDetails.name==`Preprocess`].stateExitedEventDetails.output' --output text
fi

echo "## DynamoDB (${table})"
aws dynamodb get-item --region "$region" --table-name "$table" --key "{\"jobId\":{\"S\":\"${job_id}\"}}" \
    --query 'Item.{status:status.S,updatedAt:updatedAt.S,errorReason:errorReason.S}' --output json

echo "## S3 (${bucket})"
# aws s3 ls は該当が無いと終了コード 1 を返すので、空のプレフィックスで止まらないようにする
for p in uploads work outputs; do
    aws s3 ls "s3://${bucket}/${p}/${job_id}/" --recursive || echo "(${p}/${job_id}/ は空)"
done
