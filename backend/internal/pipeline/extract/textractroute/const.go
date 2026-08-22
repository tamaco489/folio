package textractroute

// 1 本あたりの推論設定
//
// 上限は本文と参考文献を含む構造化 JSON が収まる値にし、温度を 0 にして同じ入力から同じ構造化結果を得る
//   - 19 ページ・参考文献 59 件の論文で 8192 では溢れた
//   - 生成は実測 約 36 トークン/秒で、Lambda の timeout (900 秒) 内に上限まで生成し切れる値に留める (24576 で約 680 秒)
const (
	maxTokens   int32   = 24576
	temperature float32 = 0
)
