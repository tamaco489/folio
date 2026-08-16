// Package finalize は Stage 3 の出口の Lambda (finalizer) のロジックを担う
//
// Parallel で並走した両経路の結果を S3 の規定のキーから読み、正規化 (normalize) と検証 (verify) を通して outputs/ へ永続化し、レビュー要否を決めて DynamoDB の状態を更新する
// 片方の経路だけが失敗しても成功した経路の結果は保持し、失敗の理由は comparison.json に残す (両経路の比較が目的である以上、片方でも得られる情報はある)
// 出力は S3 キーと小さな判定結果だけとし、実体は含めない (State 間の上限 256KB)
package finalize
