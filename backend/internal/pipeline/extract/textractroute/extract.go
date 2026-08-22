package textractroute

import (
	"context"
	"fmt"
	"slices"

	awstextracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"

	"github.com/tamaco489/folio/backend/internal/awsx/bedrock"
	"github.com/tamaco489/folio/backend/internal/awsx/textract"
	"github.com/tamaco489/folio/backend/internal/domain"
)

// Input は 1 本の論文を構造化するための入力
type Input struct {
	JobID    string                   // JobID は domain.Document.JobID にそのまま入る
	PaperID  string                   // PaperID は Bedrock の記録・再生キーに用いる
	Source   domain.Source            // Source は前段が確定した入力 PDF の属性であり、この経路では素通しする
	Analysis *textract.AnalysisResult // Analysis は取得済みの Textract 出力

	// FeatureTypes は Textract の呼び出しに用いた値
	//
	// 暫定は LAYOUT + TABLES だが #34 の検証で変わるため、provenance.cost には呼び出し側から渡された値をそのまま書く
	FeatureTypes []awstextracttypes.FeatureType
}

// Extractor は Read の結果を Bedrock で構造化する
type Extractor struct {
	converser bedrock.Converser
	modelID   string
}

// New は経路 A の抽出器を組み立てる
//
// modelID は provenance.cost.bedrockModel にそのまま記録するため、Converser 側の既定値には委ねず必ず受け取る
func New(c bedrock.Converser, modelID string) *Extractor {
	return &Extractor{converser: c, modelID: modelID}
}

// Extract は Textract の出力を構造化 JSON へ落とす
//
// 図と表は Read が復元した結果をそのまま用い、モデルには書誌情報・章立て・参考文献の分解だけを解釈させる
// 節の本文と参考文献の原文はモデルが指した要素番号から Reading を引いて組み立てる
// provenance の extractedAt と durationMs は後段 (normalize, finalizer) が埋めるためここでは触らない
func (e *Extractor) Extract(ctx context.Context, in Input) (*domain.Document, error) {
	if e.modelID == "" {
		return nil, fmt.Errorf("textractroute: %w", bedrock.ErrModelIDRequired)
	}

	reading, err := Read(in.Analysis)
	if err != nil {
		return nil, err
	}

	resp, err := e.converser.Converse(ctx, bedrock.Request{
		ModelID:     e.modelID,
		System:      systemPrompt,
		Messages:    []bedrock.Message{bedrock.UserText(userPrompt(reading))},
		MaxTokens:   new(maxTokens),
		Temperature: new(temperature),
		RecordKey:   bedrock.RecordKey(in.PaperID, bedrock.RouteTextract),
	})
	if err != nil {
		return nil, fmt.Errorf("textractroute: converse: %w", err)
	}

	// パースの失敗はリトライしない
	// awsx/bedrock のリトライはスロットリング用であり、同じ入力を投げ直しても JSON にならない見込みが高いうえ課金だけが増える
	var s structured
	if err := resp.DecodeJSON(&s); err != nil {
		return nil, fmt.Errorf("textractroute: decode structured response: %w", err)
	}

	sections, sectionWarns := s.sections(reading)
	references, referenceWarns := s.references(reading)
	warnings := slices.Concat(reading.Warnings, sectionWarns, referenceWarns)

	return &domain.Document{
		JobID:         in.JobID,
		SchemaVersion: domain.SchemaVersion,
		Source:        in.Source,
		Metadata:      s.metadata(),
		Sections:      sections,
		Figures:       reading.figures(),
		Tables:        reading.tables(),
		References:    references,
		Provenance: domain.Provenance{
			Route:      domain.RouteTextract,
			Confidence: reading.Confidence(),
			Cost: domain.Cost{
				TextractPages:       reading.PageCount,
				TextractFeatures:    featureNames(in.FeatureTypes),
				BedrockModel:        e.modelID,
				BedrockInputTokens:  int(resp.Usage.InputTokens),
				BedrockOutputTokens: int(resp.Usage.OutputTokens),
			},
			Warnings: warnings,
		},
	}, nil
}

func (r *Reading) figures() []domain.Figure {
	out := make([]domain.Figure, 0, len(r.Figures))
	for _, f := range r.Figures {
		out = append(out, f.Data)
	}
	return out
}

func (r *Reading) tables() []domain.Table {
	out := make([]domain.Table, 0, len(r.Tables))
	for _, t := range r.Tables {
		out = append(out, t.Data)
	}
	return out
}

func featureNames(features []awstextracttypes.FeatureType) []string {
	if len(features) == 0 {
		return nil
	}
	out := make([]string, 0, len(features))
	for _, f := range features {
		out = append(out, string(f))
	}
	return out
}
