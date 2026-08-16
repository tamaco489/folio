#!/usr/bin/env bash
#
# Lambda を provided.al2023 / arm64 向けにクロスコンパイルする
#
# 使い方
#   scripts/build.sh                       cmd/ 配下の全 Lambda をビルドする (just build)
#   scripts/build.sh pipeline/validator    指定した Lambda だけビルドする (just build-one)
#
# 引数は cmd/ からの相対パスで、複数指定できる
# 成果物は bin/{関数名}/bootstrap に置く
# 関数名は cmd/ 以下のパスをハイフンで連結して導出する (pipeline/validator -> pipeline-validator)
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

build_one() {
    local cmd="$1"
    if [ ! -f "cmd/$cmd/main.go" ]; then
        echo "main.go が見つかりません: cmd/$cmd/main.go" >&2
        exit 1
    fi
    local name
    name=$(echo "$cmd" | tr '/' '-')
    mkdir -p "bin/$name"
    # provided.al2023 は実行ファイル名が bootstrap 固定
    # lambda.norpc は aws-lambda-go の net/rpc 依存を落とす
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -tags lambda.norpc -trimpath -ldflags='-s -w' \
        -o "bin/$name/bootstrap" "./cmd/$cmd"
    echo "built: bin/$name/bootstrap"
}

if [ "$#" -gt 0 ]; then
    for cmd in "$@"; do
        build_one "$cmd"
    done
    exit 0
fi

targets=$("$script_dir/cmds.sh")
if [ -z "$targets" ]; then
    echo "ビルド対象なし: cmd/ 配下に main.go を持つディレクトリがありません"
    exit 0
fi
while read -r target; do
    build_one "$target"
done <<< "$targets"
