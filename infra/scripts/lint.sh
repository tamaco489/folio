#!/usr/bin/env bash
#
# tflint で envs/* と modules/* を検査する
#
# 使い方
#   scripts/lint.sh
#
# 設定は infra/.tflint.hcl の 1 枚だけを使う
# --recursive は各ディレクトリの .tflint.hcl を読みにいくため、絶対パスで --config に渡して全ディレクトリに同じ設定を効かせる
# 先に tflint --init を実行し、同梱されていないプラグイン (aws) を GitHub Releases から取得する
#   取得済みなら何もしない。CI では GITHUB_TOKEN を渡してレート制限を避ける
# .tf を持たないディレクトリは tflint 側で読み飛ばされる
#
# カレントディレクトリはどこでもよい (スクリプトの位置を基準に infra/ へ移動する)

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/.."

config="$PWD/.tflint.hcl"

tflint --init --config "$config"
tflint --recursive --config "$config"
