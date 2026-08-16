package bedrock

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awsbedrockruntimetypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Converse はテキストと画像のいずれの入力も同じ経路で Bedrock に送る
//
// スロットリングなどの一時的な失敗は指数バックオフでリトライする
func (c *Client) Converse(ctx context.Context, req Request) (*Response, error) {
	in, err := c.buildInput(req)
	if err != nil {
		return nil, err
	}

	attempts := max(c.retry.MaxAttempts, 1)

	var lastErr error
	for attempt := 1; ; attempt++ {
		out, err := c.api.Converse(ctx, in)
		if err == nil {
			resp, perr := newResponse(out)
			if perr != nil {
				return nil, perr
			}
			resp.Attempts = attempt
			return resp, nil
		}

		lastErr = err
		if !IsRetryable(err) {
			return nil, fmt.Errorf("bedrock: converse: %w", err)
		}
		if attempt >= attempts {
			return nil, fmt.Errorf("bedrock: converse: %w (%d attempts): %w", ErrRetryExhausted, attempts, lastErr)
		}
		if werr := c.sleep(ctx, c.retry.delay(attempt, c.randN)); werr != nil {
			return nil, fmt.Errorf("bedrock: converse: backoff interrupted: %w (last error: %w)", werr, lastErr)
		}
	}
}

func (c *Client) buildInput(req Request) (*awsbedrockruntime.ConverseInput, error) {
	modelID := req.ModelID
	if modelID == "" {
		modelID = c.defaultModelID
	}
	if modelID == "" {
		return nil, ErrModelIDRequired
	}
	if len(req.Messages) == 0 {
		return nil, ErrEmptyRequest
	}

	messages := make([]awsbedrockruntimetypes.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		blocks, err := toContentBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		role := m.Role
		if role == "" {
			role = RoleUser
		}
		messages = append(messages, awsbedrockruntimetypes.Message{
			Role:    awsbedrockruntimetypes.ConversationRole(role),
			Content: blocks,
		})
	}

	in := &awsbedrockruntime.ConverseInput{
		ModelId:  aws.String(modelID),
		Messages: messages,
	}
	if req.System != "" {
		in.System = []awsbedrockruntimetypes.SystemContentBlock{
			&awsbedrockruntimetypes.SystemContentBlockMemberText{Value: req.System},
		}
	}
	if req.MaxTokens != nil || req.Temperature != nil {
		in.InferenceConfig = &awsbedrockruntimetypes.InferenceConfiguration{
			MaxTokens:   req.MaxTokens,
			Temperature: req.Temperature,
		}
	}
	return in, nil
}

func toContentBlocks(parts []ContentPart) ([]awsbedrockruntimetypes.ContentBlock, error) {
	if len(parts) == 0 {
		return nil, ErrEmptyContent
	}
	blocks := make([]awsbedrockruntimetypes.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case TextPart:
			if v.Text == "" {
				return nil, fmt.Errorf("%w: text", ErrEmptyContent)
			}
			blocks = append(blocks, &awsbedrockruntimetypes.ContentBlockMemberText{Value: v.Text})
		case ImagePart:
			if len(v.Bytes) == 0 {
				return nil, fmt.Errorf("%w: image", ErrEmptyContent)
			}
			if v.Format == "" {
				return nil, fmt.Errorf("%w: image format", ErrEmptyContent)
			}
			blocks = append(blocks, &awsbedrockruntimetypes.ContentBlockMemberImage{
				Value: awsbedrockruntimetypes.ImageBlock{
					Format: awsbedrockruntimetypes.ImageFormat(v.Format),
					Source: &awsbedrockruntimetypes.ImageSourceMemberBytes{Value: v.Bytes},
				},
			})
		default:
			return nil, fmt.Errorf("%w: %T", ErrUnsupportedContent, p)
		}
	}
	return blocks, nil
}

func newResponse(out *awsbedrockruntime.ConverseOutput) (*Response, error) {
	if out == nil {
		return nil, ErrNoTextContent
	}

	var sb strings.Builder
	if msg, ok := out.Output.(*awsbedrockruntimetypes.ConverseOutputMemberMessage); ok {
		for _, block := range msg.Value.Content {
			if t, ok := block.(*awsbedrockruntimetypes.ContentBlockMemberText); ok {
				sb.WriteString(t.Value)
			}
		}
	}
	if sb.Len() == 0 {
		return nil, ErrNoTextContent
	}

	resp := &Response{
		Text:       sb.String(),
		StopReason: string(out.StopReason),
	}
	if out.Usage != nil {
		resp.Usage = Usage{
			InputTokens:  aws.ToInt32(out.Usage.InputTokens),
			OutputTokens: aws.ToInt32(out.Usage.OutputTokens),
			TotalTokens:  aws.ToInt32(out.Usage.TotalTokens),
		}
	}
	if out.Metrics != nil {
		resp.LatencyMs = aws.ToInt64(out.Metrics.LatencyMs)
	}
	return resp, nil
}
