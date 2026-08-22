package sfn

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	awssfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

// fakeAPI は API のフェイク (実 API は一切呼ばない)
type fakeAPI struct {
	successInputs []*awssfn.SendTaskSuccessInput
	failureInputs []*awssfn.SendTaskFailureInput
	err           error
}

var _ API = (*fakeAPI)(nil)

func (f *fakeAPI) SendTaskSuccess(_ context.Context, params *awssfn.SendTaskSuccessInput, _ ...func(*awssfn.Options)) (*awssfn.SendTaskSuccessOutput, error) {
	f.successInputs = append(f.successInputs, params)
	if f.err != nil {
		return nil, f.err
	}
	return &awssfn.SendTaskSuccessOutput{}, nil
}

func (f *fakeAPI) SendTaskFailure(_ context.Context, params *awssfn.SendTaskFailureInput, _ ...func(*awssfn.Options)) (*awssfn.SendTaskFailureOutput, error) {
	f.failureInputs = append(f.failureInputs, params)
	if f.err != nil {
		return nil, f.err
	}
	return &awssfn.SendTaskFailureOutput{}, nil
}

func TestSendTaskSuccessEncodesOutput(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	err := New(api).SendTaskSuccess(context.Background(), "token-1", struct {
		JobID     string `json:"jobId"`
		ResultKey string `json:"resultKey"`
	}{JobID: "job-1", ResultKey: "outputs/job-1/result-textract.json"})
	if err != nil {
		t.Fatalf("SendTaskSuccess: %v", err)
	}
	if len(api.successInputs) != 1 {
		t.Fatalf("calls = %d, want 1", len(api.successInputs))
	}
	in := api.successInputs[0]
	if aws.ToString(in.TaskToken) != "token-1" {
		t.Errorf("task token = %q", aws.ToString(in.TaskToken))
	}
	if want := `{"jobId":"job-1","resultKey":"outputs/job-1/result-textract.json"}`; aws.ToString(in.Output) != want {
		t.Errorf("output = %s, want %s", aws.ToString(in.Output), want)
	}
}

func TestSendTaskFailurePassesErrorAndCause(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	if err := New(api).SendTaskFailure(context.Background(), "token-1", "TextractJobFailed", `{"jobId":"job-1"}`); err != nil {
		t.Fatalf("SendTaskFailure: %v", err)
	}
	if len(api.failureInputs) != 1 {
		t.Fatalf("calls = %d, want 1", len(api.failureInputs))
	}
	in := api.failureInputs[0]
	if aws.ToString(in.TaskToken) != "token-1" || aws.ToString(in.Error) != "TextractJobFailed" || aws.ToString(in.Cause) != `{"jobId":"job-1"}` {
		t.Errorf("input = %+v", in)
	}
}

func TestSendTaskRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := map[string]func(c *Client) error{
		"異常系_SendTaskSuccess のトークンが空の場合_ErrInvalidInput が返ること": func(c *Client) error {
			return c.SendTaskSuccess(context.Background(), "", map[string]string{})
		},
		"異常系_SendTaskFailure のトークンが空の場合_ErrInvalidInput が返ること": func(c *Client) error {
			return c.SendTaskFailure(context.Background(), "", "Failed", "")
		},
		"異常系_SendTaskFailure の種別が空の場合_ErrInvalidInput が返ること": func(c *Client) error {
			return c.SendTaskFailure(context.Background(), "token-1", "", "")
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := &fakeAPI{}
			if err := call(New(api)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if len(api.successInputs)+len(api.failureInputs) != 0 {
				t.Fatal("api was called despite invalid input")
			}
		})
	}
}

// SDK が 3 型で返す「もう応答できない」を ErrTaskGone に畳めることを確かめる
func TestSendTaskFoldsGoneErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err      error
		wantGone bool
	}{
		"正常系_InvalidToken の場合_ErrTaskGone に畳まれること":     {err: &awssfntypes.InvalidToken{Message: new("Invalid Token")}, wantGone: true},
		"正常系_TaskTimedOut の場合_ErrTaskGone に畳まれること":     {err: &awssfntypes.TaskTimedOut{}, wantGone: true},
		"正常系_TaskDoesNotExist の場合_ErrTaskGone に畳まれること": {err: &awssfntypes.TaskDoesNotExist{}, wantGone: true},
		"正常系_その他のエラーの場合_ErrTaskGone にならないこと":           {err: &awssfntypes.InvalidOutput{}, wantGone: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := New(&fakeAPI{err: tt.err})

			for _, err := range []error{
				c.SendTaskSuccess(context.Background(), "token-1", map[string]string{}),
				c.SendTaskFailure(context.Background(), "token-1", "Failed", ""),
			} {
				if err == nil {
					t.Fatal("err = nil, want an error")
				}
				if got := errors.Is(err, ErrTaskGone); got != tt.wantGone {
					t.Errorf("errors.Is(err, ErrTaskGone) = %v, want %v (err = %v)", got, tt.wantGone, err)
				}
				// 畳んだ後も SDK の型で判別できること
				if !errors.Is(err, tt.err) {
					t.Errorf("err %v does not wrap the sdk error", err)
				}
			}
		})
	}
}
