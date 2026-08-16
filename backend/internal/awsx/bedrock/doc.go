// Package bedrock は Bedrock Runtime の操作を awsx 層に閉じ込める
//
// 2 つの経路は入力の種類が異なるだけで呼び出し方は同じであるため、Converse API でテキストと画像を同一の Request にまとめて扱う
//   - 経路 A: Textract の出力テキストを構造化する
//   - 経路 B: ページ画像を直接読み取る
package bedrock
