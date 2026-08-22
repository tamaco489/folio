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
# キーは固定で、同じキーへ上書きするとバケットのバージョニングが新しい version_id を振る (旧版はロールバック用の履歴)
# functions は S3 に置いた後、同じキーで各関数のコードを aws lambda update-function-code で差し替える
# Terraform は関数のコードを追跡しない (s3_object_version も source_code_hash も書かない) ので、upload だけで反映され plan に差分は出ない
# 関数がまだ無い (初回の apply 前) ときは S3 に置くだけにし、作成は Terraform に任せる
# layer は版が不変で関数側の参照の更新が要るため、従来どおり upload の後に infra の just apply で反映する
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

deploy() {
    local name="$1" key="$2"
    local function_name="${env}-folio-${name}" err
    if ! err=$(aws lambda get-function-configuration --function-name "$function_name" --query FunctionName --output text 2>&1 >/dev/null); then
        if [[ "$err" == *ResourceNotFoundException* ]]; then
            echo "skip: $function_name はまだ無いので S3 に置くだけ (初回は infra の just apply が作る)"
            return
        fi
        echo "$err" >&2
        exit 1
    fi
    aws lambda update-function-code --function-name "$function_name" --s3-bucket "$bucket" --s3-key "$key" --query CodeSha256 --output text >/dev/null
    aws lambda wait function-updated --function-name "$function_name"
    echo "deployed: $function_name"
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
        deploy "$name" "lambda/$name.zip"
    done <<< "$targets"
    ;;
layer)
    upload "layers/pdf-processor/pdf-processor.zip" "layers/pdf-processor.zip"
    ;;
esac
