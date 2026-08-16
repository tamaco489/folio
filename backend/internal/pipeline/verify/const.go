package verify

const (
	// DefaultReviewThreshold はレビュー要否の暫定の閾値
	//
	// いずれかのフィールドの信頼度がこれを下回れば要レビューとする
	// Phase 1 では実測が無いため暫定値であり、#35 の所見を受けて見直す
	DefaultReviewThreshold = 0.7

	// hallucinationThreshold は短い値 (題名、著者名、見出し、キャプション、参照文献の記載) をハルシネーションとみなす一致度の上限
	//
	// トークン対の半分以上が原本に無ければ、表記ゆれや抽出ミスではなく原本に無い値とみなす
	// 5 語の題名で真ん中の 1 語が違えば対が 2 つ崩れて 0.5 になるため、それはハルシネーション側に倒す
	hallucinationThreshold = 0.5

	// warningPrefix はこの層の警告を経路や normalize の警告と区別する接頭辞
	warningPrefix = "verify: "
)
