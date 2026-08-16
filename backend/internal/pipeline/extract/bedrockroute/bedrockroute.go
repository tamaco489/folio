// Package bedrockroute は経路 B (ページ画像を Bedrock へ直接投入する) の抽出を担う
//
// 処理は 2 段に分かれ、それぞれ独立して呼び出せる
//   - ExtractPage: ページ画像 1 枚を構造化して PageResult を返す (Step Functions の Map から 1 ページ = 1 起動で並列に走る)
//   - Merge: ページ順不同の PageResult 群を 1 つの domain.Document へ結合する (Map の外で 1 回だけ走る)
//
// ページ画像を 1 枚ずつ渡すのは Map の粒度に合わせるため
// 数ページまとめて渡せば章の連続性はモデルが把握しやすくなるが、Map の 1 起動 = 1 ページという並列化の単位と衝突する
package bedrockroute

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/domain"
)

// ページ 1 枚あたりの推論設定
//
// 表を含むページでも 1 ページ分の JSON は数千トークンに収まるため、上限は余裕を見た固定値とする
// 温度を 0 にするのは、同じページ画像から毎回同じ構造化結果を得て経路間の差分を安定させるため
const (
	maxTokens   int32   = 8192
	temperature float32 = 0
)

var (
	// ErrInvalidPage はページ番号が 1 未満の場合に返る
	ErrInvalidPage = errors.New("bedrockroute: page number must be 1 or greater")

	// ErrEmptyImage はページ画像が空の場合に返る
	ErrEmptyImage = errors.New("bedrockroute: page image is empty")

	// ErrPageDecode はモデルの応答をページ結果として解釈できない場合に返る
	//
	// この層ではリトライしない — awsx/bedrock のリトライはスロットリング向けであり、解釈できない応答を送り直すかは呼び出し側が決める
	ErrPageDecode = errors.New("bedrockroute: response is not a valid page result")
)

// PageInput は 1 ページ分の抽出への入力
type PageInput struct {
	Page     int             // Page は 1 始まりのページ番号
	Image    []byte          // Image は pdftoppm が出力した PNG のページ画像
	Language domain.Language // Language は言語ヒント (空なら指定しない)

	// RecordKey は記録・再生時のキーであり、本番の呼び出しでは空でよい
	//
	// bedrock.RecordKey は論文 ID と経路までしか表せずページを跨いで衝突するため、ページ単位で記録する場合は呼び出し側がページ番号を含めたキーを組み立てる
	RecordKey string
}

// Extractor はページ画像 1 枚を Bedrock に読ませて構造化する
type Extractor struct {
	conv    bedrock.Converser
	modelID string
}

// New は経路 B の抽出器を組み立てる
//
// modelID は provenance.cost.bedrockModel へ引き継ぐため、クライアントの既定値に委ねず注入する
func New(conv bedrock.Converser, modelID string) *Extractor {
	return &Extractor{conv: conv, modelID: modelID}
}

// ExtractPage はページ画像 1 枚を構造化する
func (e *Extractor) ExtractPage(ctx context.Context, in PageInput) (*PageResult, error) {
	if in.Page < 1 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidPage, in.Page)
	}
	if len(in.Image) == 0 {
		return nil, fmt.Errorf("%w: page %d", ErrEmptyImage, in.Page)
	}

	resp, err := e.conv.Converse(ctx, bedrock.Request{
		ModelID:     e.modelID,
		System:      systemPrompt,
		Messages:    []bedrock.Message{bedrock.UserImage(bedrock.ImageFormatPNG, in.Image, pagePrompt(in.Page, in.Language))},
		MaxTokens:   domain.Ptr(maxTokens),
		Temperature: domain.Ptr(temperature),
		RecordKey:   in.RecordKey,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrockroute: page %d: %w", in.Page, err)
	}

	var page PageResult
	if err := resp.DecodeJSON(&page); err != nil {
		return nil, fmt.Errorf("%w: page %d: %w", ErrPageDecode, in.Page, err)
	}

	// モデルはページ番号を読み違えうるため、呼び出し側が指定した番号で上書きする
	page.Page = in.Page
	page.ModelID = e.modelID
	page.Usage = resp.Usage
	return &page, nil
}
