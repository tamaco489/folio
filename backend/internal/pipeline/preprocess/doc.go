// Package preprocess は Step Functions の前処理 State のロジックを担う
//
// PDF を /tmp へ落として poppler に渡し、ページ画像とテキストレイヤーを S3 へ書き戻す
// 出力は S3 キーと判定結果だけとし、実体は含めない (State 間の上限 256KB)
package preprocess
