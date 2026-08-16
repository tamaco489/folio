# TFLint の設定 (CI とローカルの just lint で共用する)
#
# --recursive では各ディレクトリの .tflint.hcl が読まれるため、scripts/lint.sh はこのファイルを絶対パスで --config に渡す
# tflint 本体の版はルートの .tool-versions で固定する (CI の setup-tflint も同じ行を読む)

# Terraform 言語のルールセットは tflint に同梱されており、version と source は書かない
plugin "terraform" {
  enabled = true
  preset  = "recommended"
}

# AWS 固有の検査 (無効な値・ARN 形式・非推奨ランタイムなど)
# 同梱されていないため tflint --init が GitHub Releases から取得する。version は必須で v を付けない
# deep_check は使わないので AWS の認証情報は要らない
plugin "aws" {
  enabled = true
  version = "0.48.0"
  source  = "github.com/terraform-linters/tflint-ruleset-aws"
}
