package bedrockroute

// ページ 1 枚あたりの推論設定
//
// 表を含むページでも 1 ページ分の JSON は数千トークンに収まるため、上限は余裕を見た固定値とする
// 温度を 0 にするのは、同じページ画像から毎回同じ構造化結果を得て経路間の差分を安定させるため
const (
	maxTokens   int32   = 8192
	temperature float32 = 0
)
