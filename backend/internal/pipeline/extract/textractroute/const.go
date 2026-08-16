package textractroute

// 1 本あたりの推論設定
//
// 本文と参考文献を含む構造化 JSON が収まる程度に上限を取り、温度を 0 にして同じ入力から同じ構造化結果を得る
const (
	maxTokens   int32   = 8192
	temperature float32 = 0
)
