package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func testResponsesWire(t *testing.T, baseURL string) *responsesWire {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return &responsesWire{
		baseURL: parsed,
		apiKey:  "test-api-key",
		model:   "gpt-5.4-mini",
	}
}

func TestResponsesWirePayload(t *testing.T) {
	w := testResponsesWire(t, "https://chatgpt.com/backend-api/codex")

	data, err := w.Payload(ChatRequest{
		Messages:       []Message{{Role: MessageRoleUser, Content: "hello"}},
		PromptCacheKey: "session-123",
	}, true)
	if err != nil {
		t.Fatalf("Payload() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := payload["prompt_cache_key"], "session-123"; got != want {
		t.Fatalf("prompt_cache_key = %v, want %v", got, want)
	}
	if got, want := payload["store"], false; got != want {
		t.Fatalf("store = %v, want %v (OpenAI-operated host)", got, want)
	}
	if got, want := payload["stream"], true; got != want {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	// The Codex backend rejects prompt_cache_retention with a 400 (issue #318).
	if _, present := payload["prompt_cache_retention"]; present {
		t.Fatal("prompt_cache_retention present in Codex payload, want absent")
	}
}

func TestResponsesWireHTTPRequestSetsAffinityHeaders(t *testing.T) {
	tests := []struct {
		name           string
		promptCacheKey string
		wantHeaders    map[string]string
	}{
		{
			name:           "headers set when key is non-empty",
			promptCacheKey: "session-123",
			wantHeaders: map[string]string{
				"session-id": "session-123",
				"thread-id":  "session-123",
				"originator": "codex_cli_rs",
			},
		},
		{
			name:           "headers absent when key is empty",
			promptCacheKey: "",
			wantHeaders: map[string]string{
				"session-id": "",
				"thread-id":  "",
				"originator": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testResponsesWire(t, "https://api.openai.com/v1")

			req, err := w.HTTPRequest(t.Context(), ChatRequest{
				PromptCacheKey: tt.promptCacheKey,
			}, []byte("{}"), false)
			if err != nil {
				t.Fatalf("HTTPRequest() error = %v", err)
			}
			for header, want := range tt.wantHeaders {
				if got := req.Header.Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
		})
	}
}

func TestResponsesWireHTTPRequest(t *testing.T) {
	w := testResponsesWire(t, "https://chatgpt.com/backend-api/codex")
	body := []byte(`{"messages":[]}`)

	req, err := w.HTTPRequest(t.Context(), ChatRequest{PromptCacheKey: "test-session"}, body, true)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got, want := req.Method, http.MethodPost; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := req.URL.String(), "https://chatgpt.com/backend-api/codex/responses"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Accept"), "text/event-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Authorization"), "Bearer test-api-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}

	sent := new(bytes.Buffer)
	if _, err := sent.ReadFrom(req.Body); err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if got, want := sent.String(), string(body); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestResponsesWireDecodeResponse(t *testing.T) {
	w := testResponsesWire(t, "https://chatgpt.com/backend-api/codex")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{
		"status":"completed",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer"}]}],
		"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}
	}`))}

	response, err := w.DecodeResponse(resp)
	if err != nil {
		t.Fatalf("DecodeResponse() error = %v", err)
	}
	if got, want := response.Message.Content, "final answer"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v, want total tokens 18", response.Usage)
	}
}

func TestResponsesWireDecodeResponse_MalformedBodyIsNotRetryable(t *testing.T) {
	w := testResponsesWire(t, "https://chatgpt.com/backend-api/codex")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"status":`))}

	if _, err := w.DecodeResponse(resp); err == nil {
		t.Fatal("DecodeResponse() error = nil, want decode error")
	} else if decision := classifyProviderError(err, 0); decision.retry {
		t.Fatalf("decode error classified as retryable: %v", err)
	}
}

func TestResponsesWireDecodeStream(t *testing.T) {
	w := testResponsesWire(t, "https://chatgpt.com/backend-api/codex")

	body := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		``,
		`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
		``,
	}, "\n"))

	var chunks []ChatChunk
	if err := w.DecodeStream(t.Context(), body, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("DecodeStream() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("len(chunks) = %d, want at least 2", len(chunks))
	}
	if got, want := chunks[0].Delta.Content, "hello"; got != want {
		t.Fatalf("first chunk content = %q, want %q", got, want)
	}
	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if final.Usage == nil || final.Usage.TotalTokens != 18 {
		t.Fatalf("final chunk usage = %#v, want total tokens 18", final.Usage)
	}
}
