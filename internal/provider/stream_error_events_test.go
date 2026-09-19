package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const anthropicOKStream = `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}

event: message_delta
data: {"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`

func drainStream(t *testing.T, client *Client) (text, errMsg string) {
	t.Helper()
	ch, err := client.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		return "", err.Error()
	}
	for chunk := range ch {
		if chunk.Done {
			// The final chunk repeats the accumulated content.
			if chunk.Error != "" {
				errMsg = chunk.Error
			}
			continue
		}
		text += chunk.Delta.Content
		if chunk.Error != "" {
			errMsg = chunk.Error
		}
	}
	return text, errMsg
}

func TestAnthropicInBandErrorRetry(t *testing.T) {
	tests := []struct {
		name         string
		fixture      string
		wantAttempts int32
		wantErr      string
	}{
		{"overloaded is retried", "testdata/anthropic_stream_overloaded.sse", 2, ""},
		{"invalid request is terminal", "testdata/anthropic_stream_invalid_request.sse", 1, "invalid_request_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad, err := os.ReadFile(tt.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if attempts.Add(1) == 1 {
					_, _ = w.Write(bad)
					return
				}
				_, _ = w.Write([]byte(anthropicOKStream))
			}))
			defer server.Close()
			client, err := NewAnthropic(ClientConfig{
				BaseURL: server.URL + "/v1",
				Model:   "m",
				Retry:   RetryConfig{Enabled: true, MaxAttempts: 2, InitialBackoff: time.Millisecond},
			})
			if err != nil {
				t.Fatal(err)
			}
			client.sleep = func(context.Context, time.Duration) error { return nil }
			text, errMsg := drainStream(t, client)
			if got := attempts.Load(); got != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", got, tt.wantAttempts)
			}
			if tt.wantErr == "" {
				if errMsg != "" || text != "hello" {
					t.Fatalf("text = %q err = %q, want hello and no error", text, errMsg)
				}
				return
			}
			if !strings.Contains(errMsg, tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", errMsg, tt.wantErr)
			}
		})
	}
}

func TestAnthropicStreamErrorStatus(t *testing.T) {
	tests := map[string]int{
		"overloaded_error":      529,
		"api_error":             500,
		"rate_limit_error":      429,
		"invalid_request_error": 0,
		"":                      0,
	}
	for in, want := range tests {
		if got := anthropicStreamErrorStatus(in); got != want {
			t.Errorf("anthropicStreamErrorStatus(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestOpenAIStreamErrorChunkSurfacesMessage(t *testing.T) {
	body, err := os.ReadFile("testdata/openai_stream_error.sse")
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := NewOpenAICompat(ClientConfig{
		BaseURL: server.URL + "/v1",
		Model:   "m",
		Retry:   RetryConfig{Enabled: true, MaxAttempts: 2, InitialBackoff: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, errMsg := drainStream(t, client)
	if !strings.Contains(errMsg, "upstream exploded") {
		t.Fatalf("error = %q, want gateway message", errMsg)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2 (server_error is retryable)", got)
	}
}

func TestOpenAIStreamErrorToError(t *testing.T) {
	tests := []struct {
		name       string
		in         openAIStreamError
		wantStatus int
	}{
		{"numeric code", openAIStreamError{Message: "m", Code: []byte("429")}, 429},
		{"explicit status", openAIStreamError{Message: "m", Status: 503}, 503},
		{"rate limit type", openAIStreamError{Message: "m", Type: "rate_limit_error"}, 429},
		{"unknown is terminal", openAIStreamError{Message: "m", Type: "invalid_request_error", Code: []byte(`"context_length_exceeded"`)}, 0},
		{"client status not retryable", openAIStreamError{Message: "m", Status: 400}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := openAIStreamErrorToError(&tt.in)
			httpErr := asHTTPError(err)
			if tt.wantStatus == 0 {
				if httpErr != nil {
					t.Fatalf("got HTTPError %v, want plain error", httpErr)
				}
				return
			}
			if httpErr == nil || httpErr.StatusCode != tt.wantStatus {
				t.Fatalf("err = %v, want HTTPError %d", err, tt.wantStatus)
			}
			if !strings.Contains(err.Error(), "m") {
				t.Fatalf("message lost: %v", err)
			}
		})
	}
}
