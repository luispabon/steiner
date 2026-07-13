package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type temporaryTestError struct{ msg string }

func (e temporaryTestError) Error() string   { return e.msg }
func (e temporaryTestError) Timeout() bool   { return false }
func (e temporaryTestError) Temporary() bool { return true }

func TestHTTPErrorErrorFormatting(t *testing.T) {
	err := &HTTPError{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Body:       `{"error":"bad"}`,
	}
	if got, want := err.Error(), `chat completions request failed: 400 Bad Request: {"error":"bad"}`; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestReadErrorResponseReturnsHTTPError(t *testing.T) {
	p := &Client{}
	resp := &http.Response{
		Status:     "429 Too Many Requests",
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Retry-After": []string{"10"},
		},
		Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 9000))),
	}

	err := p.readErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error type = %T, want *HTTPError", err)
	}
	if got, want := httpErr.StatusCode, http.StatusTooManyRequests; got != want {
		t.Fatalf("StatusCode = %d, want %d", got, want)
	}
	if got, want := httpErr.Status, "429 Too Many Requests"; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
	if got := len(httpErr.Body); got != 1003 {
		t.Fatalf("Body length = %d, want 1003", got)
	}
	if !strings.HasSuffix(httpErr.Body, "...") {
		t.Fatalf("Body suffix = %q, want ellipsis", httpErr.Body[len(httpErr.Body)-3:])
	}
	if got, ok := retryAfterDelay(httpErr.Header, 30*time.Second); !ok || got != 10*time.Second {
		t.Fatalf("retryAfterDelay() = %v, %v, want 10s, true", got, ok)
	}
}

func TestIsRetryableHTTPStatus(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusRequestTimeout, want: true},
		{status: http.StatusConflict, want: true},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
		{status: http.StatusBadGateway, want: true},
		{status: http.StatusServiceUnavailable, want: true},
		{status: http.StatusGatewayTimeout, want: true},
		{status: http.StatusBadRequest, want: false},
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusForbidden, want: false},
		{status: http.StatusNotFound, want: false},
	}
	for _, tt := range tests {
		if got := isRetryableHTTPStatus(tt.status); got != tt.want {
			t.Fatalf("isRetryableHTTPStatus(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestIsRetryableTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, want: true},
		{name: "broken pipe text", err: errors.New("write tcp: broken pipe"), want: true},
		{name: "connection reset text", err: errors.New("read tcp: connection reset by peer"), want: true},
		{name: "http2 stream reset text", err: errors.New("http2: stream error: stream ID 1; INTERNAL_ERROR"), want: true},
		{name: "temporary net error", err: temporaryTestError{msg: "temporary"}, want: true},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: false},
		{name: "ordinary error", err: errors.New("permanent"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableTransportError(tt.err); got != tt.want {
				t.Fatalf("isRetryableTransportError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryAfterDelay(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "120")
	if got, ok := retryAfterDelay(header, 30*time.Second); !ok || got != 30*time.Second {
		t.Fatalf("retryAfterDelay() = %v, %v, want 30s, true", got, ok)
	}

	header.Set("Retry-After", "not-a-delay")
	if got, ok := retryAfterDelay(header, 30*time.Second); ok || got != 0 {
		t.Fatalf("retryAfterDelay() = %v, %v, want 0, false", got, ok)
	}
}

func TestRetryableProviderErrorParsesLiteLLMBody(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantDelay time.Duration
		wantOK    bool
	}{
		{
			name: "429 with litellm body delay",
			err: &HTTPError{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       `{"error":{"message":"litellm.RateLimitError: Try again in 28 seconds."}}`,
			},
			wantDelay: 28 * time.Second,
			wantOK:    true,
		},
		{
			name: "429 with Retry-After header takes precedence over body",
			err: &HTTPError{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       `{"error":{"message":"Try again in 28 seconds."}}`,
				Header:     http.Header{"Retry-After": []string{"10"}},
			},
			wantDelay: 10 * time.Second,
			wantOK:    true,
		},
		{
			name: "429 without parseable body returns zero delay",
			err: &HTTPError{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       `{"error":"rate limited"}`,
			},
			wantDelay: 0,
			wantOK:    true,
		},
		{
			name: "502 does not parse body",
			err: &HTTPError{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
				Body:       `Try again in 28 seconds`,
			},
			wantDelay: 0,
			wantOK:    true,
		},
		{
			name:      "400 not retryable",
			err:       &HTTPError{StatusCode: http.StatusBadRequest},
			wantDelay: 0,
			wantOK:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay, ok := RetryableProviderError(tt.err)
			if ok != tt.wantOK {
				t.Fatalf("RetryableProviderError() ok = %v, want %v", ok, tt.wantOK)
			}
			if delay != tt.wantDelay {
				t.Fatalf("RetryableProviderError() delay = %v, want %v", delay, tt.wantDelay)
			}
		})
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "429 is rate limit",
			err:  &HTTPError{StatusCode: http.StatusTooManyRequests},
			want: true,
		},
		{
			name: "502 is not rate limit",
			err:  &HTTPError{StatusCode: http.StatusBadGateway},
			want: false,
		},
		{
			name: "non-HTTP error",
			err:  errors.New("network error"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimitError(tt.err); got != tt.want {
				t.Fatalf("IsRateLimitError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRetryableTransportErrorWrappedOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: io.ErrUnexpectedEOF,
	}
	if !isRetryableTransportError(err) {
		t.Fatal("expected wrapped op error to be retryable")
	}
}
