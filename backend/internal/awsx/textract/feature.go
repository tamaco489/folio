package textract

import (
	"fmt"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

// ParseFeatureTypes は文字列の並びを FeatureType に変換する
//
// 組み合わせは未確定のため、環境変数や設定値から渡せるようにしている
func ParseFeatureTypes(values []string) ([]awstextracttypes.FeatureType, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: feature types are required", ErrInvalidInput)
	}
	features := make([]awstextracttypes.FeatureType, 0, len(values))
	for _, v := range values {
		features = append(features, awstextracttypes.FeatureType(v))
	}
	if err := ValidateFeatureTypes(features); err != nil {
		return nil, err
	}
	return features, nil
}

// ValidateFeatureTypes は SDK が定義する列挙値であるかと重複がないかを検査する
func ValidateFeatureTypes(features []awstextracttypes.FeatureType) error {
	known := map[awstextracttypes.FeatureType]struct{}{}
	for _, v := range awstextracttypes.FeatureType("").Values() {
		known[v] = struct{}{}
	}
	seen := map[awstextracttypes.FeatureType]struct{}{}
	for _, f := range features {
		if _, ok := known[f]; !ok {
			return fmt.Errorf("%w: unknown feature type %q", ErrInvalidInput, string(f))
		}
		if _, dup := seen[f]; dup {
			return fmt.Errorf("%w: duplicated feature type %q", ErrInvalidInput, string(f))
		}
		seen[f] = struct{}{}
	}
	return nil
}
