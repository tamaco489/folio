// Package bedrockparser は経路 B のページ単位の抽出 State のロジックを担う
//
// Step Functions の Map から 1 ページ = 1 起動で並列に呼ばれるため、責務をページ画像 1 枚の取得・抽出・保存に限る
// 言語判定・ページ数の判定・S3 の一覧取得・ページ結果の結合は持たない (Map の中に置くと並列度の制御と責務分離が崩れる)
// 出力は S3 キーだけとし、実体は S3 に置く (State 間の上限 256KB)
package bedrockparser
