package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testOpenAIWire(t *testing.T, baseURL, model string) *openaiWire {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return &openaiWire{baseURL: parsed, model: model}
}

func TestOpenAIWirePayload_StreamFlags(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")
	request := ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}

	nonStream, err := w.Payload(request, false)
	if err != nil {
		t.Fatalf("Payload() error = %v", err)
	}
	if got, want := string(nonStream), `{"messages":[{"role":"user","content":"hello"}],"model":"gpt-4"}`; got != want {
		t.Fatalf("non-stream payload = %s, want %s", got, want)
	}

	stream, err := w.Payload(request, true)
	if err != nil {
		t.Fatalf("Payload() error = %v", err)
	}
	if got, want := string(stream), `{"messages":[{"role":"user","content":"hello"}],"model":"gpt-4","stream":true,"stream_options":{"include_usage":true}}`; got != want {
		t.Fatalf("stream payload = %s, want %s", got, want)
	}
}

func TestOpenAIWirePayload_MessagesAndExtraParams(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")

	data, err := w.Payload(ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "hi there"},
		},
		ExtraParams: map[string]any{
			"temperature": 0.7,
			"top_p":       0.9,
		},
	}, false)
	if err != nil {
		t.Fatalf("Payload() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := payload["temperature"], 0.7; got != want {
		t.Fatalf("temperature = %v, want %v", got, want)
	}
	if got, want := payload["top_p"], 0.9; got != want {
		t.Fatalf("top_p = %v, want %v", got, want)
	}
	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %#v, want two messages", payload["messages"])
	}
}

func TestOpenAIWirePayload_Store(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		wantStore bool
	}{
		{name: "openai host sets store false", baseURL: "https://api.openai.com/v1", wantStore: true},
		{name: "generic compat host omits store", baseURL: "http://localhost:11434/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testOpenAIWire(t, tt.baseURL, "test-model")
			data, err := w.Payload(ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
			}, false)
			if err != nil {
				t.Fatalf("Payload() error = %v", err)
			}

			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			store, ok := payload["store"]
			if ok != tt.wantStore {
				t.Fatalf("store present = %v, want %v", ok, tt.wantStore)
			}
			if tt.wantStore && store != false {
				t.Fatalf("store = %v, want false", store)
			}
		})
	}
}

func TestOpenAIWirePayload_PromptCacheKey(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		cacheKey     string
		wantPresent  bool
	}{
		{name: "openai emits key when set", providerType: "openai", cacheKey: "session-123", wantPresent: true},
		{name: "openai omits key when unset", providerType: "openai", cacheKey: "", wantPresent: false},
		{name: "ollama omits key even when set", providerType: "ollama", cacheKey: "session-123", wantPresent: false},
		{name: "lmstudio omits key even when set", providerType: "lmstudio", cacheKey: "session-123", wantPresent: false},
		{name: "openai_compat omits key even when set", providerType: "openai_compat", cacheKey: "session-123", wantPresent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")
			w.providerType = tt.providerType

			data, err := w.Payload(ChatRequest{
				Messages:       []Message{{Role: MessageRoleUser, Content: "hello"}},
				PromptCacheKey: tt.cacheKey,
			}, false)
			if err != nil {
				t.Fatalf("Payload() error = %v", err)
			}

			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			key, ok := payload["prompt_cache_key"]
			if ok != tt.wantPresent {
				t.Fatalf("prompt_cache_key present = %v, want %v", ok, tt.wantPresent)
			}
			if tt.wantPresent && key != tt.cacheKey {
				t.Fatalf("prompt_cache_key = %v, want %v", key, tt.cacheKey)
			}
		})
	}
}

func TestOpenAIWireHTTPRequest(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")
	w.apiKey = "secret"
	w.headers = map[string]string{
		"X-Test-Header": "configured",
		"Authorization": "Bearer override",
	}

	req, err := w.HTTPRequest(t.Context(), ChatRequest{PromptCacheKey: "ignored-key"}, []byte(`{}`), true)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got, want := req.URL.String(), "http://localhost:11434/v1/chat/completions"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
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
	// Cache-shard affinity headers belong to the Codex Responses wire only.
	for _, header := range []string{"session-id", "thread-id", "originator"} {
		if got := req.Header.Get(header); got != "" {
			t.Fatalf("%s = %q, want empty for the OpenAI wire", header, got)
		}
	}
}

func TestOpenAIWireHTTPRequest_TrailingSlashBaseURL(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1/", "gpt-4")

	req, err := w.HTTPRequest(t.Context(), ChatRequest{}, []byte(`{}`), false)
	if err != nil {
		t.Fatalf("HTTPRequest() error = %v", err)
	}
	if got, want := req.URL.String(), "http://localhost:11434/v1/chat/completions"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestOpenAIWireDecodeResponse(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{
		"choices":[{"message":{"role":"assistant","content":"final answer"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
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

func TestOpenAIWireDecodeStream(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")

	body := strings.NewReader(`data: {"choices":[{"delta":{"content":"hello"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}

data: [DONE]

`)

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
	if chunks[1].Usage == nil || chunks[1].Usage.TotalTokens != 18 {
		t.Fatalf("final chunk usage = %#v, want total tokens 18", chunks[1].Usage)
	}
}

func TestOpenAIWireRefineRetry(t *testing.T) {
	rateLimited := func(body string, header http.Header) *HTTPError {
		return &HTTPError{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Body:       body,
			Header:     header,
		}
	}

	tests := []struct {
		name           string
		providerType   string
		err            error
		decision       retryDecision
		wantRetry      bool
		wantRetryAfter time.Duration
	}{
		{
			name:         "litellm budget exhaustion is permanent",
			providerType: "litellm",
			err:          rateLimited("Budget has been exceeded!", nil),
			decision:     retryDecision{retry: true, reason: "429"},
		},
		{
			name:           "litellm parses retry delay from body when header is absent",
			providerType:   "litellm",
			err:            rateLimited("Rate limit reached. Try again in 11 seconds", nil),
			decision:       retryDecision{retry: true, reason: "429"},
			wantRetry:      true,
			wantRetryAfter: 11 * time.Second,
		},
		{
			name:           "litellm keeps the Retry-After header delay",
			providerType:   "litellm",
			err:            rateLimited("Try again in 11 seconds", http.Header{"Retry-After": []string{"3"}}),
			decision:       retryDecision{retry: true, reason: "429", retryAfter: 3 * time.Second},
			wantRetry:      true,
			wantRetryAfter: 3 * time.Second,
		},
		{
			name:           "litellm leaves non-429 errors alone",
			providerType:   "litellm",
			err:            &HTTPError{StatusCode: http.StatusServiceUnavailable, Body: "Budget has been exceeded"},
			decision:       retryDecision{retry: true, reason: "503"},
			wantRetry:      true,
			wantRetryAfter: 0,
		},
		{
			name:         "plain openai ignores litellm body semantics",
			providerType: "openai",
			err:          rateLimited("Budget has been exceeded. Try again in 11 seconds", nil),
			decision:     retryDecision{retry: true, reason: "429"},
			wantRetry:    true,
		},
		{
			name:         "already non-retryable decisions are untouched",
			providerType: "litellm",
			err:          rateLimited("Try again in 11 seconds", nil),
			decision:     retryDecision{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testOpenAIWire(t, "http://localhost:4000/v1", "gpt-4")
			w.providerType = tt.providerType

			got := w.RefineRetry(tt.err, tt.decision)
			if got.retry != tt.wantRetry {
				t.Fatalf("retry = %v, want %v", got.retry, tt.wantRetry)
			}
			if got.retryAfter != tt.wantRetryAfter {
				t.Fatalf("retryAfter = %v, want %v", got.retryAfter, tt.wantRetryAfter)
			}
		})
	}
}

// TestClientClassifyRetryError_LiteLLMRefinementCapsRetryAfter checks that a
// delay recovered from a litellm error body is still bounded by RetryAfterMax,
// which the engine applies after the wire refines the decision.
func TestClientClassifyRetryError_LiteLLMRefinementCapsRetryAfter(t *testing.T) {
	c := &Client{
		retry: RetryConfig{RetryAfterMax: 5 * time.Second},
		wire:  &openaiWire{providerType: "litellm"},
	}

	decision := c.classifyRetryError(&HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Body:       "Try again in 120 seconds",
	})
	if !decision.retry {
		t.Fatal("retry = false, want true")
	}
	if got, want := decision.retryAfter, 5*time.Second; got != want {
		t.Fatalf("retryAfter = %v, want %v (capped by RetryAfterMax)", got, want)
	}
}

func TestOpenAIWirePayload_StreamNonStreamParity(t *testing.T) {
	maxTokens := 200
	tools := []ToolSpec{
		{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:        "get_weather",
				Description: "Get the weather for a location",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "What's the weather?"},
		},
		MaxTokens: &maxTokens,
		Tools:     tools,
		ExtraParams: map[string]any{
			"temperature": 0.7,
			"top_p":       0.9,
		},
	}

	nonStreamData, err := w.Payload(req, false)
	if err != nil {
		t.Fatalf("non-stream Payload() error: %v", err)
	}
	var nonStream map[string]any
	if err := json.Unmarshal(nonStreamData, &nonStream); err != nil {
		t.Fatalf("non-stream unmarshal error: %v", err)
	}

	streamData, err := w.Payload(req, true)
	if err != nil {
		t.Fatalf("stream Payload() error: %v", err)
	}
	var stream map[string]any
	if err := json.Unmarshal(streamData, &stream); err != nil {
		t.Fatalf("stream unmarshal error: %v", err)
	}

	if nonStream["model"] != stream["model"] {
		t.Fatalf("model mismatch: non-stream=%v, stream=%v", nonStream["model"], stream["model"])
	}
	if nonStream["model"] != "gpt-4" {
		t.Fatalf("model = %v, want %v", nonStream["model"], "gpt-4")
	}

	nonStreamMsgs, ok := nonStream["messages"].([]any)
	if !ok {
		t.Fatal("non-stream messages is not an array")
	}
	streamMsgs, ok := stream["messages"].([]any)
	if !ok {
		t.Fatal("stream messages is not an array")
	}
	if len(nonStreamMsgs) != len(streamMsgs) {
		t.Fatalf("messages count mismatch: %d vs %d", len(nonStreamMsgs), len(streamMsgs))
	}
	if len(nonStreamMsgs) != 1 {
		t.Fatalf("messages length = %d, want 1", len(nonStreamMsgs))
	}
	firstMsg, ok := nonStreamMsgs[0].(map[string]any)
	if !ok {
		t.Fatal("message is not a map")
	}
	if firstMsg["role"] != "user" {
		t.Fatalf("role = %v, want %v", firstMsg["role"], "user")
	}
	if firstMsg["content"] != "What's the weather?" {
		t.Fatalf("content = %v, want %v", firstMsg["content"], "What's the weather?")
	}

	if nonStream["max_tokens"] != stream["max_tokens"] {
		t.Fatalf("max_tokens mismatch: %v vs %v", nonStream["max_tokens"], stream["max_tokens"])
	}
	if nonStream["max_tokens"] != float64(200) {
		t.Fatalf("max_tokens = %v, want %v", nonStream["max_tokens"], 200)
	}

	nonStreamTools, ok := nonStream["tools"].([]any)
	if !ok {
		t.Fatal("non-stream tools missing")
	}
	streamTools, ok := stream["tools"].([]any)
	if !ok {
		t.Fatal("stream tools missing")
	}
	if len(nonStreamTools) != len(streamTools) {
		t.Fatalf("tools length mismatch: %d vs %d", len(nonStreamTools), len(streamTools))
	}

	if nonStream["temperature"] != stream["temperature"] {
		t.Fatalf("temperature mismatch: %v vs %v", nonStream["temperature"], stream["temperature"])
	}
	if nonStream["top_p"] != stream["top_p"] {
		t.Fatalf("top_p mismatch: %v vs %v", nonStream["top_p"], stream["top_p"])
	}

	if _, ok := nonStream["stream"]; ok {
		t.Fatal("non-stream payload should not have stream key")
	}
	if _, ok := nonStream["stream_options"]; ok {
		t.Fatal("non-stream payload should not have stream_options key")
	}

	streamVal, ok := stream["stream"]
	if !ok {
		t.Fatal("stream payload should have stream key")
	}
	if streamVal != true {
		t.Fatalf("stream = %v, want true", streamVal)
	}
	so, ok := stream["stream_options"]
	if !ok {
		t.Fatal("stream payload should have stream_options key")
	}
	soMap, ok := so.(map[string]any)
	if !ok {
		t.Fatal("stream_options should be a map")
	}
	iu, ok := soMap["include_usage"]
	if !ok {
		t.Fatal("stream_options should have include_usage")
	}
	if iu != true {
		t.Fatalf("include_usage = %v, want true", iu)
	}
}

func TestOpenAIWireDecodeResponse_Malformed(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")

	resp := &http.Response{Body: io.NopCloser(strings.NewReader("not valid json"))}

	_, err := w.DecodeResponse(resp)
	if err == nil {
		t.Fatal("DecodeResponse() error = nil, want decode error")
	}
	if !errors.Is(err, errDecodeChatCompletionResponse) {
		t.Fatalf("error = %q, want sentinel match", err.Error())
	}
	if decision := classifyProviderError(err, 0); decision.retry {
		t.Fatalf("decode error classified as retryable: %v", err)
	}
}

func TestOpenAIWireDecodeStream_Malformed(t *testing.T) {
	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")

	err := w.DecodeStream(t.Context(), strings.NewReader("data: not valid json\n\n"), func(ChatChunk) error {
		return nil
	})
	if err == nil {
		t.Fatal("DecodeStream() error = nil, want decode error")
	}
}
