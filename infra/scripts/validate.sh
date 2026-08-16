#!/usr/bin/env bash
#
# envs/{env} と modules/* を terraform validate する
#
# 使い方
#   scripts/validate.sh          envs/dev と modules/* を検証する (just validate)
#   scripts/validate.sh stg      envs/stg と modules/* を検証する (just env=stg validate)
#
# init は -backend=false で行い、state バケットや AWS の認証情報を使わない (CI と同じ条件)
# envs/{env} は .terraform.lock.hcl の provider 版をそのまま使う
# modules/* は envs/{env} から結線されていなくても個別に検証する (作成中のモジュールを CI で拾うため)
#   子モジュールの lock は Terraform が参照しないため持たず、init -upgrade で制約内の最新 provider を取る
#   init が生成する modules/*/.terraform.lock.hcl はコミットしない (.gitignore で除外している)
# .tf を持たないディレクトリ (.gitkeep だけの雛形) は読み飛ばす
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に infra/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

env="${1:-dev}"

validate_dir() {
    local dir="$1"
    shift
    echo "==> $dir"
    terraform -chdir="$dir" init -backend=false -input=false "$@"
    terraform -chdir="$dir" validate
}

if [ ! -d "envs/$env" ]; then
    echo "環境ディレクトリが見つかりません: envs/$env" >&2
    exit 1
fi
validate_dir "envs/$env"

for dir in modules/*/; do
    dir="${dir%/}"
    if ! compgen -G "$dir/*.tf" > /dev/null; then
        echo "==> $dir (skip: .tf なし)"
        continue
    fi
    validate_dir "$dir" -upgrade
done
