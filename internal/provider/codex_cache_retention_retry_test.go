package provider

import (
	"net/http"
	"testing"
)

// codexPromptCacheRetentionBody is the exact 400 body observed from the
// Codex/ChatGPT OAuth backend (steiner-delegation.log, 2026-08-23).
const codexPromptCacheRetentionBody = `{
  "error": {
    "message": "prompt_cache_retention is not supported on this model",
    "type": "invalid_request_error",
    "param": "prompt_cache_retention",
    "code": "invalid_parameter"
  }
}`

func TestIsCodexPromptCacheRetentionRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "matches the observed backend rejection",
			err:  &HTTPError{StatusCode: http.StatusBadRequest, Body: codexPromptCacheRetentionBody},
			want: true,
		},
		{
			name: "different param on the same status is not the quirk",
			err: &HTTPError{StatusCode: http.StatusBadRequest, Body: `{
  "error": {
    "message": "temperature is not supported on this model",
    "type": "invalid_request_error",
    "param": "temperature",
    "code": "invalid_parameter"
  }
}`},
			want: false,
		},
		{
			name: "same message text but non-400 status is not the quirk",
			err:  &HTTPError{StatusCode: http.StatusServiceUnavailable, Body: codexPromptCacheRetentionBody},
			want: false,
		},
		{
			name: "non-JSON body does not match",
			err:  &HTTPError{StatusCode: http.StatusBadRequest, Body: "prompt_cache_retention is not supported on this model"},
			want: false,
		},
		{
			name: "non-HTTPError is not the quirk",
			err:  errDecodeChatCompletionResponse,
			want: false,
		},
		{
			name: "nil error is not the quirk",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCodexPromptCacheRetentionRejection(tt.err); got != tt.want {
				t.Fatalf("isCodexPromptCacheRetentionRejection() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResponsesWireRefineRetry(t *testing.T) {
	w := &responsesWire{}

	t.Run("turns the known quirk retryable", func(t *testing.T) {
		err := &HTTPError{StatusCode: http.StatusBadRequest, Body: codexPromptCacheRetentionBody}
		got := w.RefineRetry(err, retryDecision{})
		if !got.retry {
			t.Fatalf("retry = %v, want true", got.retry)
		}
	})

	t.Run("leaves an unrelated 400 non-retryable", func(t *testing.T) {
		err := &HTTPError{StatusCode: http.StatusBadRequest, Body: `{"error":{"param":"model","code":"invalid_parameter"}}`}
		got := w.RefineRetry(err, retryDecision{})
		if got.retry {
			t.Fatalf("retry = %v, want false", got.retry)
		}
	})

	t.Run("does not override an already-retryable decision", func(t *testing.T) {
		err := &HTTPError{StatusCode: http.StatusServiceUnavailable}
		in := retryDecision{retry: true, reason: "503"}
		got := w.RefineRetry(err, in)
		if got != in {
			t.Fatalf("decision = %+v, want unchanged %+v", got, in)
		}
	})
}
