package textractparser

import (
	"strings"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/textract"
)

// ParseFeatureTypes はカンマ区切りの設定値 (config.Config.TextractFeatureTypes) を FeatureTypes に変換する
func ParseFeatureTypes(spec string) ([]awstextracttypes.FeatureType, error) {
	var values []string
	for v := range strings.SplitSeq(spec, ",") {
		if v = strings.TrimSpace(v); v != "" {
			values = append(values, v)
		}
	}
	return textract.ParseFeatureTypes(values)
}
