package provider

import (
	"net/http"
	"testing"
	"time"
)

// F395: RetryableProviderError must cap the Retry-After header value to prevent arbitrary server-supplied delays.
func TestRetryableProviderErrorCapsRetryAfterHeader(t *testing.T) {
	err := &HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Retry-After": []string{"300"},
		},
	}
	delay, ok := RetryableProviderError(err)
	if !ok {
		t.Fatal("RetryableProviderError should recognize 429 as retryable")
	}
	if delay != retryAfterMaxDefault {
		t.Fatalf("delay = %v, want %v (capped)", delay, retryAfterMaxDefault)
	}
	if delay > 60*time.Second {
		t.Fatalf("delay %v exceeds cap of 60s", delay)
	}
}

// F395: RetryableProviderError must cap the LiteLLM body retry-after value to prevent arbitrary delays.
func TestRetryableProviderErrorCapsLiteLLMRetryAfter(t *testing.T) {
	err := &HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Body: `{
			"detail": [{"type": "rate_limit_exceeded"}],
			"retry_after": 300
		}`,
		Header: http.Header{},
	}
	delay, ok := RetryableProviderError(err)
	if !ok {
		t.Fatal("RetryableProviderError should recognize 429 as retryable")
	}
	if delay != retryAfterMaxDefault {
		t.Fatalf("delay = %v, want %v (capped)", delay, retryAfterMaxDefault)
	}
	if delay > 60*time.Second {
		t.Fatalf("delay %v exceeds cap of 60s", delay)
	}
}

// F395: RetryableProviderError should respect delays below the cap.
func TestRetryableProviderErrorRespectsBelowCapDelay(t *testing.T) {
	err := &HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Retry-After": []string{"10"},
		},
	}
	delay, ok := RetryableProviderError(err)
	if !ok {
		t.Fatal("RetryableProviderError should recognize 429 as retryable")
	}
	if delay != 10*time.Second {
		t.Fatalf("delay = %v, want 10s", delay)
	}
}
