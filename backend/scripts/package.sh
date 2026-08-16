#!/usr/bin/env bash
#
# ビルド成果物を配布用の zip に固める
#
# 使い方
#   scripts/package.sh
#
# bin/{関数名}/bootstrap を bin/{関数名}.zip に固める
# ビルド自体はここでは行わない。just の依存宣言 (package: build) が先に build を走らせる
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

targets=$("$script_dir/cmds.sh")
if [ -z "$targets" ]; then
    echo "パッケージ対象なし: ビルド成果物がありません"
    exit 0
fi
while read -r target; do
    name=$(echo "$target" | tr '/' '-')
    # zip 直下に bootstrap を置く必要があるため成果物ディレクトリで固める
    # -X はタイムスタンプ以外の追加属性を除いて差分を安定させる
    (cd "bin/$name" && zip -q -X "../$name.zip" bootstrap)
    echo "packaged: bin/$name.zip"
done <<< "$targets"
