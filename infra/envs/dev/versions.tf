terraform {
  # 1.15 系に閉じる (>= 1.15.0, < 1.16.0)
  # 新しいマイナーが書いた state は古いバイナリで読めなくなるため、.tool-versions のマイナーに揃える
  # パッチまでは固定しない
  # 厳密な版はルートの .tool-versions (1.15.8) が持つ
  required_version = "~> 1.15.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.60"
    }
  }
}
