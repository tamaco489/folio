package bedrock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// fakeSleeper は待機を記録するだけで実時間を消費しない
type fakeSleeper struct {
	waited []time.Duration
	err    error
}

func (s *fakeSleeper) sleep(_ context.Context, d time.Duration) error {
	s.waited = append(s.waited, d)
	return s.err
}

func throttling() error {
	return &types.ThrottlingException{Message: aws.String("Too many requests")}
}

func testRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 4,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		Jitter:      false,
	}
}

// スロットリングが続いた後に成功する場合、待機が指数的に伸びることを確かめる
func TestConverseRetriesOnThrottling(t *testing.T) {
	api := &fakeAPI{
		errs: []error{throttling(), throttling(), nil},
		outputs: []*bedrockruntime.ConverseOutput{
			nil, nil, textOutput(`{"ok":true}`),
		},
	}
	sleeper := &fakeSleeper{}
	c := New(api,
		WithDefaultModelID("m"),
		WithRetry(testRetryConfig()),
		WithSleeper(sleeper.sleep),
	)

	resp, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if api.calls() != 3 {
		t.Errorf("api calls = %d, want 3", api.calls())
	}
	if resp.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", resp.Attempts)
	}

	want := []time.Duration{500 * time.Millisecond, time.Second}
	if len(sleeper.waited) != len(want) {
		t.Fatalf("waited = %v, want %v", sleeper.waited, want)
	}
	for i, d := range want {
		if sleeper.waited[i] != d {
			t.Errorf("waited[%d] = %v, want %v", i, sleeper.waited[i], d)
		}
	}
}

// 待機時間が MaxDelay で頭打ちになることを確かめる
func TestConverseBackoffCappedAtMaxDelay(t *testing.T) {
	api := &fakeAPI{errs: []error{throttling(), throttling(), throttling(), throttling()}}
	sleeper := &fakeSleeper{}
	c := New(api,
		WithDefaultModelID("m"),
		WithRetry(testRetryConfig()),
		WithSleeper(sleeper.sleep),
	)

	_, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}})
	if !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("err = %v, want ErrRetryExhausted", err)
	}
	if api.calls() != 4 {
		t.Errorf("api calls = %d, want 4 (MaxAttempts)", api.calls())
	}

	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	if len(sleeper.waited) != len(want) {
		t.Fatalf("waited = %v, want %v", sleeper.waited, want)
	}
	for i, d := range want {
		if sleeper.waited[i] != d {
			t.Errorf("waited[%d] = %v, want %v", i, sleeper.waited[i], d)
		}
	}

	var throttled *types.ThrottlingException
	if !errors.As(err, &throttled) {
		t.Errorf("最後のエラーが原因として残っていない: %v", err)
	}
}

// full jitter が有効な場合、待機時間が [0, 指数バックオフ値] に収まることを確かめる
func TestConverseBackoffJitter(t *testing.T) {
	tests := []struct {
		name  string
		randN func(int64) int64
		want  []time.Duration
	}{
		{
			name:  "乱数が下限",
			randN: func(int64) int64 { return 0 },
			want:  []time.Duration{0, 0},
		},
		{
			name:  "乱数が中央",
			randN: func(n int64) int64 { return n / 2 },
			want: []time.Duration{
				time.Duration((int64(500*time.Millisecond) + 1) / 2),
				time.Duration((int64(time.Second) + 1) / 2),
			},
		},
		{
			name:  "乱数が上限",
			randN: func(n int64) int64 { return n - 1 },
			want:  []time.Duration{500 * time.Millisecond, time.Second},
		},
	}

	upper := []time.Duration{500 * time.Millisecond, time.Second}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{errs: []error{throttling(), throttling(), nil}}
			sleeper := &fakeSleeper{}
			rc := testRetryConfig()
			rc.Jitter = true

			c := New(api,
				WithDefaultModelID("m"),
				WithRetry(rc),
				WithSleeper(sleeper.sleep),
				WithRandN(tt.randN),
			)

			if _, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}}); err != nil {
				t.Fatalf("Converse: %v", err)
			}
			if len(sleeper.waited) != len(tt.want) {
				t.Fatalf("waited = %v, want %d 件", sleeper.waited, len(tt.want))
			}
			for i, d := range sleeper.waited {
				if d != tt.want[i] {
					t.Errorf("waited[%d] = %v, want %v", i, d, tt.want[i])
				}
				if d < 0 || d > upper[i] {
					t.Errorf("waited[%d] = %v, want within [0, %v]", i, d, upper[i])
				}
			}
		})
	}
}

// 入力起因の失敗はリトライしない
func TestConverseDoesNotRetryValidationError(t *testing.T) {
	api := &fakeAPI{errs: []error{&types.ValidationException{Message: aws.String("bad input")}}}
	sleeper := &fakeSleeper{}
	c := New(api,
		WithDefaultModelID("m"),
		WithRetry(testRetryConfig()),
		WithSleeper(sleeper.sleep),
	)

	_, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}})
	if err == nil {
		t.Fatal("err = nil, want validation error")
	}
	if errors.Is(err, ErrRetryExhausted) {
		t.Errorf("リトライしてはならないエラーでリトライしている: %v", err)
	}
	if api.calls() != 1 {
		t.Errorf("api calls = %d, want 1", api.calls())
	}
	if len(sleeper.waited) != 0 {
		t.Errorf("waited = %v, want none", sleeper.waited)
	}
}

// ctx がキャンセルされた場合はバックオフ中でも打ち切る
func TestConverseAbortsWhenSleeperFails(t *testing.T) {
	api := &fakeAPI{errs: []error{throttling(), throttling()}}
	sleeper := &fakeSleeper{err: context.Canceled}
	c := New(api,
		WithDefaultModelID("m"),
		WithRetry(testRetryConfig()),
		WithSleeper(sleeper.sleep),
	)

	_, err := c.Converse(context.Background(), Request{Messages: []Message{UserText("x")}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if api.calls() != 1 {
		t.Errorf("api calls = %d, want 1", api.calls())
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "throttling", err: &types.ThrottlingException{}, want: true},
		{name: "service unavailable", err: &types.ServiceUnavailableException{}, want: true},
		{name: "internal server", err: &types.InternalServerException{}, want: true},
		{name: "model timeout", err: &types.ModelTimeoutException{}, want: true},
		{name: "model not ready", err: &types.ModelNotReadyException{}, want: true},
		{name: "validation", err: &types.ValidationException{}, want: false},
		{name: "access denied", err: &types.AccessDeniedException{}, want: false},
		{name: "quota exceeded", err: &types.ServiceQuotaExceededException{}, want: false},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "wrapped throttling", err: errors.Join(errors.New("outer"), &types.ThrottlingException{}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.want {
				t.Errorf("IsRetryable = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryConfigDelayDefaults(t *testing.T) {
	rc := RetryConfig{MaxAttempts: 3}
	got := rc.delay(1, nil)
	if got != DefaultRetryConfig().BaseDelay {
		t.Errorf("delay = %v, want %v", got, DefaultRetryConfig().BaseDelay)
	}
}
