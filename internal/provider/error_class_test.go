package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
)

// fakeTimeoutError implements net.Error with Timeout() true, the shape a
// dial or read deadline produces.
type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string   { return "fake: i/o timeout" }
func (fakeTimeoutError) Timeout() bool   { return true }
func (fakeTimeoutError) Temporary() bool { return true }

func TestClassifyErrorClass(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 0)
	defer deadlineCancel()
	<-deadlineCtx.Done()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want errorClass
	}{
		{
			name: "nil error",
			ctx:  context.Background(),
			err:  nil,
			want: "",
		},
		{
			name: "caller cancelled context",
			ctx:  cancelledCtx,
			err:  fmt.Errorf("read response: %w", context.Canceled),
			want: errorClassContextCancelled,
		},
		{
			name: "caller deadline exceeded",
			ctx:  deadlineCtx,
			err:  fmt.Errorf("read response: %w", context.DeadlineExceeded),
			want: errorClassContextCancelled,
		},
		{
			name: "decode chat completion response",
			ctx:  context.Background(),
			err:  errDecodeChatCompletionResponse,
			want: errorClassDecodeError,
		},
		{
			name: "decode tool call arguments",
			ctx:  context.Background(),
			err:  fmt.Errorf("wrap: %w", errDecodeToolCallArguments),
			want: errorClassDecodeError,
		},
		{
			name: "decode stream chunk unexpected",
			ctx:  context.Background(),
			err:  errDecodeStreamChunkUnexpected,
			want: errorClassDecodeError,
		},
		{
			name: "http 429 rate limited",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"},
			want: errorClassRateLimited,
		},
		{
			name: "http 500",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusInternalServerError, Status: "500"},
			want: errorClassHTTP5xx,
		},
		{
			name: "http 503",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusServiceUnavailable, Status: "503"},
			want: errorClassHTTP5xx,
		},
		{
			name: "http 404",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusNotFound, Status: "404"},
			want: errorClassHTTP4xx,
		},
		{
			name: "net.Error timeout without caller cancellation",
			ctx:  context.Background(),
			err:  fakeTimeoutError{},
			want: errorClassTimeout,
		},
		{
			name: "text timeout without caller cancellation",
			ctx:  context.Background(),
			err:  errors.New("dial tcp: i/o timeout"),
			want: errorClassTimeout,
		},
		{
			name: "connection reset by peer",
			ctx:  context.Background(),
			err:  &net.OpError{Op: "read", Err: errors.New("connection reset by peer")},
			want: errorClassConnectionReset,
		},
		{
			name: "unexpected eof",
			ctx:  context.Background(),
			err:  fmt.Errorf("read response: %w", fmt.Errorf("unexpected EOF")),
			want: errorClassConnectionReset,
		},
		{
			name: "uncategorized error",
			ctx:  context.Background(),
			err:  errors.New("something odd happened"),
			want: errorClassOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyErrorClass(tt.ctx, tt.err)
			if got != tt.want {
				t.Errorf("classifyErrorClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBoundedError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := boundedError(nil); got != "" {
			t.Errorf("boundedError(nil) = %q, want empty", got)
		}
	})

	t.Run("short error passes through", func(t *testing.T) {
		err := errors.New("boom")
		if got := boundedError(err); got != "boom" {
			t.Errorf("boundedError() = %q, want %q", got, "boom")
		}
	})

	t.Run("long error is truncated", func(t *testing.T) {
		long := ""
		for i := 0; i < 500; i++ {
			long += "x"
		}
		got := boundedError(errors.New(long))
		if len(got) != maxErrorLength {
			t.Errorf("len(boundedError()) = %d, want %d", len(got), maxErrorLength)
		}
	})
}

// timeoutSensitiveDialError exercises the net.OpError unwrap path with a
// wrapped timeout, mirroring a real dial timeout's shape.
func TestClassifyErrorClassNetOpErrorTimeout(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: fakeTimeoutError{}}
	if got := classifyErrorClass(context.Background(), err); got != errorClassTimeout {
		t.Errorf("classifyErrorClass() = %q, want %q", got, errorClassTimeout)
	}
}
