package bedrock

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// ErrRetryExhausted はリトライ上限に達した場合に返る
var ErrRetryExhausted = errors.New("bedrock: retry attempts exhausted")

// Sleeper はバックオフの待機を担う (テストで実時間を待たないよう差し替えられるようにしている)
type Sleeper func(ctx context.Context, d time.Duration) error

// RealSleeper は実時間を待つ既定の実装 (ctx のキャンセルで打ち切る)
func RealSleeper(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RetryConfig は指数バックオフの設定
type RetryConfig struct {
	MaxAttempts int           // MaxAttempts は初回を含む試行回数の上限
	BaseDelay   time.Duration // BaseDelay は 1 回目のリトライ前の待機時間
	MaxDelay    time.Duration // MaxDelay は 1 回あたりの待機時間の上限

	// Jitter は待機時間に full jitter を掛けるかどうか
	//
	// Map で並列起動された bedrock-parser が同時に再試行するとスロットリングが再発するため、既定では有効にする
	Jitter bool
}

// DefaultRetryConfig はスロットリングを前提とした既定値を返す
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    20 * time.Second,
		Jitter:      true,
	}
}

func defaultRandN(n int64) int64 { return rand.Int64N(n) }

// delay は attempt 回目の試行が失敗した後に待つ時間を返す
func (rc RetryConfig) delay(attempt int, randN func(int64) int64) time.Duration {
	base := rc.BaseDelay
	if base <= 0 {
		base = DefaultRetryConfig().BaseDelay
	}
	maxDelay := rc.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRetryConfig().MaxDelay
	}

	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxDelay {
			d = maxDelay
			break
		}
	}
	if d > maxDelay {
		d = maxDelay
	}
	if !rc.Jitter || randN == nil || d <= 0 {
		return d
	}
	return time.Duration(randN(int64(d) + 1))
}

// IsRetryable は一時的な失敗かどうかを判定する
//
// スロットリングに加え、モデル側の一時的な不調 (5xx 相当) も対象とする
// 以下は対象外とする
//   - ValidationException や AccessDeniedException のような入力・権限起因の失敗
//   - ServiceQuotaExceededException のような引き上げ申請を要する失敗
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var throttling *types.ThrottlingException
	if errors.As(err, &throttling) {
		return true
	}
	var unavailable *types.ServiceUnavailableException
	if errors.As(err, &unavailable) {
		return true
	}
	var internal *types.InternalServerException
	if errors.As(err, &internal) {
		return true
	}
	var timeout *types.ModelTimeoutException
	if errors.As(err, &timeout) {
		return true
	}
	var notReady *types.ModelNotReadyException
	return errors.As(err, &notReady)
}
