package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatNewOpenAICompat_EmptyBaseURL(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "",
		Model:     "gpt-4",
		Scheduler: &Scheduler{},
	})
	if err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestOpenAICompatNewOpenAICompat_InvalidURL(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "://invalid",
		Model:     "gpt-4",
		Scheduler: &Scheduler{},
	})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestOpenAICompatNewOpenAICompat_EmptyModel(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "http://localhost:11434/v1",
		Model:     "",
		Scheduler: &Scheduler{},
	})
	if err == nil {
		t.Fatal("expected error for empty model")
	}
}

func TestOpenAICompatNewOpenAICompat_NilScheduler(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL: "http://localhost:11434/v1",
		Model:   "gpt-4",
	})
	if err == nil {
		t.Fatal("expected error for nil scheduler")
	}
}

func TestOpenAICompatNewOpenAICompat_DefaultHTTPClient(t *testing.T) {
	p, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "http://localhost:11434/v1",
		Model:     "gpt-4",
		Scheduler: &Scheduler{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.httpClient != defaultHTTPClient {
		t.Fatal("expected default HTTP client when none provided")
	}
}

func TestOpenAICompatNewOpenAICompat_Success(t *testing.T) {
	customClient := &http.Client{}
	cfg := OpenAICompatConfig{
		BaseURL:    "http://localhost:11434/v1",
		APIKey:     "test-key",
		Headers:    map[string]string{"X-Test-Header": "configured"},
		Model:      "gpt-4",
		HTTPClient: customClient,
		Scheduler:  &Scheduler{},
	}
	p, err := NewOpenAICompat(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL.String() != "http://localhost:11434/v1" {
		t.Fatalf("baseURL = %q, want %q", p.baseURL.String(), "http://localhost:11434/v1")
	}
	if p.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want %q", p.apiKey, "test-key")
	}
	if p.model != "gpt-4" {
		t.Fatalf("model = %q, want %q", p.model, "gpt-4")
	}
	if p.httpClient != customClient {
		t.Fatal("expected custom HTTP client to be stored")
	}
	if got, want := p.headers["X-Test-Header"], "configured"; got != want {
		t.Fatalf("headers[X-Test-Header] = %q, want %q", got, want)
	}
}

func TestOpenAICompatNewOpenAICompat_TimeoutClonesHTTPClient(t *testing.T) {
	customClient := &http.Client{}
	p, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    45 * time.Second,
		HTTPClient: customClient,
		Scheduler:  &Scheduler{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.httpClient == customClient {
		t.Fatal("expected timeout configuration to clone HTTP client")
	}
	if got, want := p.httpClient.Timeout, 45*time.Second; got != want {
		t.Fatalf("httpClient.Timeout = %v, want %v", got, want)
	}
	if customClient.Timeout != 0 {
		t.Fatalf("original client timeout = %v, want 0", customClient.Timeout)
	}
}

// TestOpenAICompatNewOpenAICompat_TimeoutDisablesResponseHeaderTimeout
// ensures the transport's ResponseHeaderTimeout does not shadow the
// provider's Client.Timeout. Without clearing it, a slow upstream that takes
// longer than the hardcoded 30s transport header timeout would still return
// `net/http: timeout awaiting response headers`, ignoring the user's
// configured provider timeout.
func TestOpenAICompatNewOpenAICompat_TimeoutDisablesResponseHeaderTimeout(t *testing.T) {
	originalTransport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	customClient := &http.Client{Transport: originalTransport}
	p, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    45 * time.Second,
		HTTPClient: customClient,
		Scheduler:  &Scheduler{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.httpClient.Transport == originalTransport {
		t.Fatal("expected transport to be cloned, not shared with original client")
	}
	cloned, ok := p.httpClient.Transport.(*http.Transport)
	if !ok || cloned == nil {
		t.Fatalf("expected transport to be *http.Transport, got %T", p.httpClient.Transport)
	}
	if cloned.ResponseHeaderTimeout != 0 {
		t.Fatalf("cloned ResponseHeaderTimeout = %v, want 0 (Client.Timeout covers the whole request)", cloned.ResponseHeaderTimeout)
	}
	if originalTransport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("original transport ResponseHeaderTimeout = %v, want 30s (must not be mutated)", originalTransport.ResponseHeaderTimeout)
	}
}

func TestOpenAICompatSupportsUsageStats_NonNil(t *testing.T) {
	p := &OpenAICompat{}
	if !p.SupportsUsageStats() {
		t.Fatal("expected true for provider")
	}
}

func TestOpenAICompatSupportsUsageStats_Nil(t *testing.T) {
	var p *OpenAICompat
	if !p.SupportsUsageStats() {
		t.Fatal("expected true for nil provider")
	}
}

func TestOpenAICompatReadErrorResponse_EmptyBody(t *testing.T) {
	p := &OpenAICompat{}
	resp := &http.Response{
		Status:     "400 Bad Request",
		StatusCode: 400,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
	err := p.readErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "chat completions request failed: 400 Bad Request"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestOpenAICompatReadErrorResponse_WithBody(t *testing.T) {
	p := &OpenAICompat{}
	resp := &http.Response{
		Status:     "400 Bad Request",
		StatusCode: 400,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"bad"}`))),
	}
	err := p.readErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	want := `chat completions request failed: 400 Bad Request: {"error":"bad"}`
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestOpenAICompatMarshalRequest_NoStream(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
	}
	data, err := p.marshalRequest(req, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got, want := m["model"], "gpt-4"; got != want {
		t.Fatalf("model = %v, want %v", got, want)
	}
	if _, ok := m["stream"]; ok {
		t.Fatal("stream key should not be present when false (omitempty)")
	}
	if _, ok := m["stream_options"]; ok {
		t.Fatal("stream_options should not be present when stream is false")
	}
}

func TestOpenAICompatMarshalRequest_WithStream(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
	}
	data, err := p.marshalRequest(req, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got, want := m["model"], "gpt-4"; got != want {
		t.Fatalf("model = %v, want %v", got, want)
	}
	if got, want := m["stream"], true; got != want {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	so, ok := m["stream_options"]
	if !ok {
		t.Fatal("stream_options should be present when stream is true")
	}
	soMap, ok := so.(map[string]any)
	if !ok {
		t.Fatal("stream_options should be a map")
	}
	iu, ok := soMap["include_usage"]
	if !ok {
		t.Fatal("stream_options.include_usage should be present")
	}
	if iu != true {
		t.Fatalf("stream_options.include_usage = %v, want true", iu)
	}
}

func TestOpenAICompatMarshalRequest_MessagesAndExtraParams(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "hi there"},
		},
		ExtraParams: map[string]any{
			"temperature": 0.7,
			"top_p":       0.9,
		},
	}
	data, err := p.marshalRequest(req, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got, want := m["temperature"], 0.7; got != want {
		t.Fatalf("temperature = %v, want %v", got, want)
	}
	if got, want := m["top_p"], 0.9; got != want {
		t.Fatalf("top_p = %v, want %v", got, want)
	}
	msgs, ok := m["messages"].([]any)
	if !ok {
		t.Fatal("messages should be an array")
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
}

func TestOpenAICompatMarshalRequest_Characterization(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
	}

	nonStream, err := p.marshalRequest(req, false)
	if err != nil {
		t.Fatalf("marshalRequest() error = %v", err)
	}
	if got, want := string(nonStream), `{"messages":[{"role":"user","content":"hello"}],"model":"gpt-4"}`; got != want {
		t.Fatalf("non-stream payload = %s, want %s", got, want)
	}

	stream, err := p.marshalRequest(req, true)
	if err != nil {
		t.Fatalf("marshalRequest() error = %v", err)
	}
	if got, want := string(stream), `{"messages":[{"role":"user","content":"hello"}],"model":"gpt-4","stream":true,"stream_options":{"include_usage":true}}`; got != want {
		t.Fatalf("stream payload = %s, want %s", got, want)
	}
}

func TestOpenAICompatMarshalRequest_OpenAIHostSetsStoreFalse(t *testing.T) {
	parsed, err := url.Parse("https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	p := &OpenAICompat{baseURL: parsed, model: "gpt-5.4-mini"}
	data, err := p.marshalRequest(ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}, false)
	if err != nil {
		t.Fatalf("marshalRequest() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := payload["store"], false; got != want {
		t.Fatalf("store = %v, want %v", got, want)
	}
}

func TestOpenAICompatMarshalRequest_GenericCompatHostOmitsStore(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	p := &OpenAICompat{baseURL: parsed, model: "local-model"}
	data, err := p.marshalRequest(ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}, false)
	if err != nil {
		t.Fatalf("marshalRequest() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := payload["store"]; ok {
		t.Fatal("store present for generic compat provider, want omitted")
	}
}

func TestOpenAICompatNormalizeChatResponse_Characterization(t *testing.T) {
	response, err := normalizeChatResponse(&openAIResponse{
		Choices: []openAIChoice{
			{
				Message: openAIMessage{
					Role:    "assistant",
					Content: "final answer",
				},
				FinishReason: "stop",
			},
		},
		Usage: &UsageStats{
			PromptTokens:     11,
			CompletionTokens: 7,
			TotalTokens:      18,
		},
	})
	if err != nil {
		t.Fatalf("normalizeChatResponse() error = %v", err)
	}
	if got, want := response.Message.Role, MessageRoleAssistant; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}
	if got, want := response.Message.Content, "final answer"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got, want := response.FinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if response.Usage == nil {
		t.Fatal("usage = nil, want usage stats")
	}
	if got, want := response.Usage.TotalTokens, 18; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
}

func TestOpenAICompatDecodeChatStream_Characterization(t *testing.T) {
	body := strings.NewReader(`data: {"choices":[{"delta":{"content":"hello"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}

data: [DONE]

`)

	var chunks []ChatChunk
	err := decodeChatStreamWithHandler(t.Context(), body, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
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
	if got, want := chunks[1].Delta.Content, "hello"; got != want {
		t.Fatalf("final chunk content = %q, want %q", got, want)
	}
	if got, want := chunks[1].FinishReason, "stop"; got != want {
		t.Fatalf("final chunk finish reason = %q, want %q", got, want)
	}
	if chunks[1].Usage == nil || chunks[1].Usage.TotalTokens != 18 {
		t.Fatalf("final chunk usage = %#v, want total tokens 18", chunks[1].Usage)
	}
}

func TestOpenAICompatBuildHTTPRequestAppliesConfiguredHeaders(t *testing.T) {
	parsed, _ := url.Parse("http://localhost:11434/v1")
	p := &OpenAICompat{
		baseURL: parsed,
		apiKey:  "secret",
		headers: map[string]string{
			"X-Test-Header": "configured",
			"Authorization": "Bearer override",
		},
	}

	req, err := p.buildHTTPRequest(t.Context(), []byte(`{}`), true)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got, want := req.Header.Get("X-Test-Header"), "configured"; got != want {
		t.Fatalf("X-Test-Header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Authorization"), "Bearer override"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Accept"), "text/event-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
}

func TestOpenAICompatChatCompletionsURL_NoTrailingSlash(t *testing.T) {
	parsed, _ := url.Parse("http://localhost:11434/v1")
	p := &OpenAICompat{baseURL: parsed}
	got := p.chatCompletionsURL()
	want := "http://localhost:11434/v1/chat/completions"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpenAICompatChatCompletionsURL_TrailingSlash(t *testing.T) {
	parsed, _ := url.Parse("http://localhost:11434/v1/")
	p := &OpenAICompat{baseURL: parsed}
	got := p.chatCompletionsURL()
	want := "http://localhost:11434/v1/chat/completions"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpenAICompatClassifyRetryError_ToolCallDecodeIsRetryable(t *testing.T) {
	p := &OpenAICompat{}
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "decode tool call error is retryable",
			err:       fmt.Errorf("%w %q arguments: %w", errDecodeToolCallArguments, "mutate", errors.New("invalid character '}' after array element")),
			wantRetry: true,
		},
		{
			name:      "decode chat completion response is not retryable",
			err:       fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, io.ErrUnexpectedEOF),
			wantRetry: false,
		},
		{
			name:      "nil error",
			err:       nil,
			wantRetry: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := p.classifyRetryError(tt.err)
			if d.retry != tt.wantRetry {
				t.Fatalf("retry = %v, want %v", d.retry, tt.wantRetry)
			}
		})
	}
}
