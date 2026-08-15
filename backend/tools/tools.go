//go:build tools

// Package tools は go mod tidy が依存を削除しないよう保持するためだけに存在する
//
// aws-lambda-go は最初の main.go (#16) が入るまでコードから参照されない
// 参照先ができた時点でこのファイルは不要になる
package tools

import _ "github.com/aws/aws-lambda-go/lambda"
