// Package sfn は Step Functions の操作を awsx 層に閉じ込める
//
// コールバックパターン (waitForTaskToken) で待つ Task へ、タスクトークンを使って完了と失敗を応答するためにある
package sfn
