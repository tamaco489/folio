// Package validate は Step Functions の入口で入力の妥当性と冪等性を判定する
//
// 判定結果は Choice が分岐に使えるよう Output.Decision に載せ、Lambda のエラーとしては返さない
// エラーとして返すのはイベント通知の設定ミスと AWS 呼び出しの失敗だけで、これらは Catch とリトライの対象になる
package validate
