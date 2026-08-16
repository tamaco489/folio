#!/usr/bin/env bash
#
# ビルド成果物を削除する
#
# 使い方
#   scripts/clean.sh
#
# bin/ 配下の bootstrap と *.zip を消し、空になったディレクトリ (bin/ 自身を含む) も消す
# 生成物以外のファイルが bin/ にあれば残す
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に backend/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

if [ ! -d bin ]; then
    echo "削除対象なし: bin/ はありません"
    exit 0
fi
find bin -type f \( -name bootstrap -o -name '*.zip' \) -delete
find bin -depth -type d -empty -delete
echo "cleaned: bin/"
