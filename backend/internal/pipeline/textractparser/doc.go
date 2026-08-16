// Package textractparser は経路 A の Lambda (textract-parser) のロジックを担う
//
// Textract の非同期解析は起動から完了まで Lambda の実行時間を超えうるため、待ち合わせは Step Functions のコールバックパターン (waitForTaskToken) で行う
// 1 つの Lambda が 2 種類のイベントを受け、種類で分岐する
//   - 起動 (start): Step Functions から呼ばれ、Textract を開始してタスクトークンを S3 へ退避する
//   - 完了通知 (callback): SNS から呼ばれ、結果を取得・構造化して SendTaskSuccess / SendTaskFailure で State Machine を再開させる
//
// 出力は S3 キーと小さな判定結果だけとし、実体は含めない (State 間の上限 256KB)
package textractparser
