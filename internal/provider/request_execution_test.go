package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBuildRequestPayload_Parity(t *testing.T) {
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

	p := &OpenAICompat{model: "gpt-4"}
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

	nonStreamData, err := p.buildRequestPayload(req, false)
	if err != nil {
		t.Fatalf("non-stream buildRequestPayload error: %v", err)
	}
	var nonStream map[string]any
	if err := json.Unmarshal(nonStreamData, &nonStream); err != nil {
		t.Fatalf("non-stream unmarshal error: %v", err)
	}

	streamData, err := p.buildRequestPayload(req, true)
	if err != nil {
		t.Fatalf("stream buildRequestPayload error: %v", err)
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

func TestBuildHTTPRequest(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	p := &OpenAICompat{baseURL: parsed, apiKey: "test-key"}
	ctx := context.Background()
	body := []byte(`{"model":"gpt-4"}`)

	req, err := p.buildHTTPRequest(ctx, body, false)
	if err != nil {
		t.Fatalf("non-stream buildHTTPRequest error: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want %q", req.Method, http.MethodPost)
	}
	if req.URL.String() != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("URL = %q, want %q", req.URL.String(), "http://localhost:11434/v1/chat/completions")
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", req.Header.Get("Content-Type"), "application/json")
	}
	if req.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want %q", req.Header.Get("Authorization"), "Bearer test-key")
	}
	if req.Header.Get("Accept") != "" {
		t.Fatal("Accept header should not be present for non-stream")
	}

	req, err = p.buildHTTPRequest(ctx, body, true)
	if err != nil {
		t.Fatalf("stream buildHTTPRequest error: %v", err)
	}
	if req.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("Accept = %q, want %q", req.Header.Get("Accept"), "text/event-stream")
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", req.Header.Get("Content-Type"), "application/json")
	}
	if req.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want %q", req.Header.Get("Authorization"), "Bearer test-key")
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want %q", req.Method, http.MethodPost)
	}
	if req.URL.String() != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("URL = %q, want %q", req.URL.String(), "http://localhost:11434/v1/chat/completions")
	}
}

func TestBuildHTTPRequest_NoAuth(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	p := &OpenAICompat{baseURL: parsed, apiKey: ""}
	ctx := context.Background()
	body := []byte(`{"model":"gpt-4"}`)

	req, err := p.buildHTTPRequest(ctx, body, false)
	if err != nil {
		t.Fatalf("buildHTTPRequest error: %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("Authorization header should not be present when apiKey is empty")
	}
}

func TestDecodeNonStreamResponse_Success(t *testing.T) {
	p := &OpenAICompat{}
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`))),
	}
	payload, err := p.decodeNonStreamResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payload.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(payload.Choices))
	}
	content, _ := payload.Choices[0].Message.Content.(string)
	if content != "Hello!" {
		t.Fatalf("content = %q, want %q", content, "Hello!")
	}
	if payload.Usage == nil {
		t.Fatal("usage should not be nil")
	}
	if payload.Usage.TotalTokens != 30 {
		t.Fatalf("TotalTokens = %d, want %d", payload.Usage.TotalTokens, 30)
	}
}

func TestDecodeNonStreamResponse_Malformed(t *testing.T) {
	p := &OpenAICompat{}
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader([]byte(`not valid json`))),
	}
	_, err := p.decodeNonStreamResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "decode chat completion response") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "decode chat completion response")
	}
}

func TestDecodeStreamResponse_Success(t *testing.T) {
	p := &OpenAICompat{}
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: [DONE]\n\n"
	reader := strings.NewReader(body)
	out := make(chan ChatChunk, 10)
	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.decodeStreamResponse(ctx, reader, out)
		close(out)
	}()

	var chunks []ChatChunk
	for chunk := range out {
		chunks = append(chunks, chunk)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want at least 2", len(chunks))
	}

	var hasContent bool
	for _, chunk := range chunks {
		if chunk.Delta.Content != "" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatal("expected at least one chunk with content delta")
	}

	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Done {
		t.Fatal("expected last chunk to have Done=true")
	}
}

func TestDecodeStreamResponse_Malformed(t *testing.T) {
	p := &OpenAICompat{}
	body := "data: not valid json\n\n"
	reader := strings.NewReader(body)
	out := make(chan ChatChunk, 10)
	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.decodeStreamResponse(ctx, reader, out)
		close(out)
	}()

	for range out {
	}

	if err := <-errCh; err == nil {
		t.Fatal("expected error for malformed stream data")
	}
}

func TestReadErrorResponse(t *testing.T) {
	tests := []struct {
		name   string
		status string
		body   string
		want   string
	}{
		{
			name:   "empty body",
			status: "400 Bad Request",
			body:   "",
			want:   "chat completions request failed: 400 Bad Request",
		},
		{
			name:   "with body",
			status: "400 Bad Request",
			body:   `{"error":"bad"}`,
			want:   `chat completions request failed: 400 Bad Request: {"error":"bad"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &OpenAICompat{}
			resp := &http.Response{
				Status:     tt.status,
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewReader([]byte(tt.body))),
			}
			err := p.readErrorResponse(resp)
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Fatalf("got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
