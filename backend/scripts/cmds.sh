#!/usr/bin/env bash
#
# ビルド対象を列挙する (cmd/ からの相対パス。例: pipeline/validator)
#
# 使い方
#   scripts/cmds.sh
#
# cmd/ 配下で main.go を持つディレクトリを 1 行 1 件で標準出力に書く
# cmd/ が無ければ何も出さず 0 で終わる
# build.sh と package.sh がこの出力を対象一覧として使う
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

[ -d cmd ] || exit 0
find cmd -type f -name main.go | sed -e 's#^cmd/##' -e 's#/main\.go$##' | sort
