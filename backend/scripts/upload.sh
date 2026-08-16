#!/usr/bin/env bash
#
# 配布用の zip を artifacts バケットへアップロードする
#
# 使い方
#   scripts/upload.sh functions [env]   bin/{関数名}.zip を lambda/{関数名}.zip へ (関数名は cmds.sh の一覧から導く)
#   scripts/upload.sh layer [env]       layers/pdf-processor/pdf-processor.zip を layers/pdf-processor.zip へ
#
# env の既定は dev。バケットは {env}-folio-artifacts-{アカウント ID} で、Terraform (infra/modules/storage) が作る
# アカウント ID は環境変数 TF_VAR_account_id から取り、未設定なら aws sts get-caller-identity で調べる
# AWS の認証情報は AWS_PROFILE などで用意する
#
# キーは固定で、同じキーへ上書きするとバケットのバージョニングが新しい version_id を振る
# Terraform はその version_id を s3_object_version で参照し、次の plan が関数 (と Layer) の更新を検出する
# Lambda の更新は Terraform の apply で行い、このスクリプトは S3 に置くところまでを担う
#
# zip 自体は作らない。functions は just の依存宣言 (upload: package) が先に package を走らせ、layer は layers/pdf-processor/build.sh で作っておく
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

kind="${1:-}"
env="${2:-dev}"

case "$kind" in
functions | layer) ;;
*)
    echo "使い方: scripts/upload.sh functions|layer [env]" >&2
    exit 1
    ;;
esac

account_id="${TF_VAR_account_id:-}"
if [ -z "$account_id" ]; then
    account_id=$(aws sts get-caller-identity --query Account --output text)
fi
if ! [[ "$account_id" =~ ^[0-9]{12}$ ]]; then
    echo "アカウント ID が 12 桁の数字ではありません: $account_id" >&2
    exit 1
fi

bucket="${env}-folio-artifacts-${account_id}"

upload() {
    local src="$1" key="$2"
    if [ ! -f "$src" ]; then
        echo "zip がありません: $src" >&2
        exit 1
    fi
    aws s3 cp "$src" "s3://$bucket/$key"
}

case "$kind" in
functions)
    targets=$("$script_dir/cmds.sh")
    if [ -z "$targets" ]; then
        echo "アップロード対象なし: cmd/ に main.go がありません"
        exit 0
    fi
    while read -r target; do
        name=$(echo "$target" | tr '/' '-')
        upload "bin/$name.zip" "lambda/$name.zip"
    done <<< "$targets"
    ;;
layer)
    upload "layers/pdf-processor/pdf-processor.zip" "layers/pdf-processor.zip"
    ;;
esac
