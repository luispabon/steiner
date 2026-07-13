package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func testAnthropicWire(t *testing.T, apiKey string, headers map[string]string) *anthropicWire {
	t.Helper()
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return &anthropicWire{
		baseURL: parsed,
		apiKey:  apiKey,
		headers: headers,
		model:   "claude-3-7-sonnet",
	}
}

func TestAnthropicWirePayload(t *testing.T) {
	w := testAnthropicWire(t, "test-key", nil)

	body, err := w.Payload(ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}, true)
	if err != nil {
		t.Fatalf("Payload() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := payload["model"], "claude-3-7-sonnet"; got != want {
		t.Fatalf("model = %v, want %v (wire default model)", got, want)
	}
	if got, want := payload["stream"], true; got != want {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}

func TestAnthropicWireHTTPRequest(t *testing.T) {
	w := testAnthropicWire(t, "test-key", nil)

	req, err := w.HTTPRequest(t.Context(), ChatRequest{}, []byte(`{"model":"claude-3-7-sonnet"}`), false)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got, want := req.Method, http.MethodPost; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := req.URL.String(), "http://localhost:11434/v1/messages"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("x-api-key"), "test-key"; got != want {
		t.Fatalf("x-api-key = %q, want %q", got, want)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got, want := req.Header.Get("anthropic-version"), "2023-06-01"; got != want {
		t.Fatalf("anthropic-version = %q, want %q", got, want)
	}
	if got := req.Header.Get("Accept"); got != "" {
		t.Fatalf("Accept = %q, want empty", got)
	}
}

func TestAnthropicWireHTTPRequest_Stream(t *testing.T) {
	w := testAnthropicWire(t, "test-key", nil)

	req, err := w.HTTPRequest(t.Context(), ChatRequest{}, []byte(`{"model":"claude-3-7-sonnet"}`), true)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got, want := req.Header.Get("Accept"), "text/event-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("x-api-key"), "test-key"; got != want {
		t.Fatalf("x-api-key = %q, want %q", got, want)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got, want := req.Header.Get("anthropic-version"), "2023-06-01"; got != want {
		t.Fatalf("anthropic-version = %q, want %q", got, want)
	}
}

func TestAnthropicWireHTTPRequest_NoAuth(t *testing.T) {
	w := testAnthropicWire(t, "", nil)

	req, err := w.HTTPRequest(t.Context(), ChatRequest{}, []byte(`{"model":"claude-3-7-sonnet"}`), false)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
}

func TestAnthropicWireHTTPRequest_HeaderOverrides(t *testing.T) {
	w := testAnthropicWire(t, "test-key", map[string]string{
		"x-api-key":         "override-key",
		"anthropic-version": "2024-01-01",
		"Authorization":     "Manual test auth",
		"Content-Type":      "application/custom+json",
		"Accept":            "application/custom-stream",
	})

	req, err := w.HTTPRequest(t.Context(), ChatRequest{}, []byte(`{"model":"claude-3-7-sonnet"}`), true)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got, want := req.Header.Get("x-api-key"), "override-key"; got != want {
		t.Fatalf("x-api-key = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("anthropic-version"), "2024-01-01"; got != want {
		t.Fatalf("anthropic-version = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Authorization"), "Manual test auth"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Content-Type"), "application/custom+json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Accept"), "application/custom-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
}

func TestAnthropicWireDecodeResponse(t *testing.T) {
	w := testAnthropicWire(t, "test-key", nil)

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{
		"role":"assistant",
		"content":[{"type":"text","text":"final answer"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":11,"output_tokens":7}
	}`))}

	response, err := w.DecodeResponse(resp)
	if err != nil {
		t.Fatalf("DecodeResponse() error = %v", err)
	}
	if got, want := response.Message.Content, "final answer"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got, want := response.FinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v, want total tokens 18", response.Usage)
	}
}

func TestAnthropicWireDecodeResponse_MalformedBodyIsNotRetryable(t *testing.T) {
	w := testAnthropicWire(t, "test-key", nil)

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"role":`))}

	if _, err := w.DecodeResponse(resp); err == nil {
		t.Fatal("DecodeResponse() error = nil, want decode error")
	} else if decision := classifyProviderError(err, 0); decision.retry {
		t.Fatalf("decode error classified as retryable: %v", err)
	}
}

func TestAnthropicWireDecodeStream(t *testing.T) {
	w := testAnthropicWire(t, "test-key", nil)

	body := strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":7}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n"))

	var chunks []ChatChunk
	if err := w.DecodeStream(t.Context(), body, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("DecodeStream() error = %v", err)
	}
	if got, want := len(chunks), 2; got != want {
		t.Fatalf("len(chunks) = %d, want %d", got, want)
	}
	if got, want := chunks[0].Delta.Content, "hello"; got != want {
		t.Fatalf("first chunk content = %q, want %q", got, want)
	}
	if !chunks[1].Done {
		t.Fatal("final chunk Done = false, want true")
	}
}
