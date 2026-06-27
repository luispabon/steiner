package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCodexResponsesChatCompletionPostsToResponsesWithOAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))
	}))
	defer server.Close()

	prov := newTestCodexResponsesWithAPIKey(t, server.URL, server.Client(), "sk-codex")
	resp, err := prov.ChatCompletion(context.Background(), ChatRequest{
		Model: "override-model",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "system one"},
			{Role: MessageRoleSystem, Content: "system two"},
			{Role: MessageRoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want /responses", gotPath)
	}
	if gotAuth != "Bearer sk-codex" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotPayload["model"] != "override-model" {
		t.Fatalf("model = %v, want override-model", gotPayload["model"])
	}
	if gotPayload["instructions"] != "system one\n\nsystem two" {
		t.Fatalf("instructions = %q", gotPayload["instructions"])
	}
	if resp.Message.Content != "done" {
		t.Fatalf("content = %q, want done", resp.Message.Content)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %#v, want total 5", resp.Usage)
	}
}

func TestCodexResponsesToolsToolCallsToolOutputsAndImages(t *testing.T) {
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"x\"}"}]}`))
	}))
	defer server.Close()

	prov := newTestCodexResponses(t, server.URL, server.Client())
	resp, err := prov.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "abc"}}},
			{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: map[string]any{"query": "x"}}}},
			{Role: MessageRoleTool, ToolCallID: "call_1", Content: "result"},
		},
		Tools: []ToolSpec{{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:        "lookup",
				Description: "Lookup",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	tools := gotPayload["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "lookup" || tool["type"] != "function" {
		t.Fatalf("tool = %#v", tool)
	}
	if strict, ok := tool["strict"]; !ok || strict != false {
		t.Fatalf("tool strict = %v (present=%v), want false", strict, ok)
	}
	input := gotPayload["input"].([]any)
	user := input[0].(map[string]any)
	content := user["content"].([]any)
	if content[1].(map[string]any)["image_url"] != "data:image/png;base64,abc" {
		t.Fatalf("image content = %#v", content[1])
	}
	call := input[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_1" {
		t.Fatalf("function call input = %#v", call)
	}
	if call["id"] != nil {
		t.Fatalf("function_call input id = %v, want absent (API requires fc_ prefix for id, but we use call_id for matching)", call["id"])
	}
	output := input[2].(map[string]any)
	if output["type"] != "function_call_output" || output["output"] != "result" {
		t.Fatalf("function output input = %#v", output)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v", resp.Message.ToolCalls)
	}
}

func TestCodexResponsesHTMLProviderErrorIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><head><title>Just a moment...</title><script>" + strings.Repeat("x", 5000) + "</script></head><body><h1>Forbidden</h1>" + strings.Repeat("y", 5000) + "</body></html>"))
	}))
	defer server.Close()

	prov := newTestCodexResponses(t, server.URL, server.Client())
	_, err := prov.ChatCompletion(context.Background(), ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want 403")
	}
	text := err.Error()
	if len(text) > 1200 {
		t.Fatalf("error length = %d, want bounded: %q", len(text), text)
	}
	if strings.Contains(text, "<html") || strings.Contains(text, "<script") {
		t.Fatalf("error contains raw HTML: %q", text)
	}
	if !strings.Contains(text, "Just a moment") || !strings.Contains(text, "Forbidden") {
		t.Fatalf("error = %q, want summarized HTML text", text)
	}
}

func TestCodexResponsesStreamNormalizesChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"{\\\"query\\\":\\\"x\\\"}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":3,\"total_tokens\":7}}}\n\n"))
	}))
	defer server.Close()

	prov := newTestCodexResponses(t, server.URL, server.Client())
	ch, err := prov.StreamChatCompletion(context.Background(), ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	var chunks []ChatChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks len = %d, want 3: %#v", len(chunks), chunks)
	}
	if chunks[0].Delta.Content != "hel" || chunks[1].Delta.Content != "lo" {
		t.Fatalf("delta chunks = %#v", chunks[:2])
	}
	final := chunks[2]
	if !final.Done || final.Delta.Content != "hello" {
		t.Fatalf("final chunk = %#v", final)
	}
	if len(final.Delta.ToolCalls) != 1 || final.Delta.ToolCalls[0].ID != "call_1" {
		t.Fatalf("final tool calls = %#v", final.Delta.ToolCalls)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 7 {
		t.Fatalf("final usage = %#v", final.Usage)
	}
}

func TestCodexResponsesBuildResponsesPayload_OpenAIHostSetsStoreFalse(t *testing.T) {
	parsed, err := url.Parse("https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	prov := &CodexResponses{OpenAICompat: &OpenAICompat{
		baseURL: parsed,
		model:   "gpt-5.5",
	}}

	data, err := prov.buildResponsesPayload(ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesPayload() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := payload["store"], false; got != want {
		t.Fatalf("store = %v, want %v", got, want)
	}
}

func newTestCodexResponses(t *testing.T, baseURL string, client *http.Client) *CodexResponses {
	return newTestCodexResponsesWithAPIKey(t, baseURL, client, "")
}

func newTestCodexResponsesWithAPIKey(t *testing.T, baseURL string, client *http.Client, apiKey string) *CodexResponses {
	t.Helper()
	scheduler, err := NewScheduler(1)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	prov, err := NewCodexResponses(OpenAICompatConfig{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      "gpt-5.5",
		HTTPClient: client,
		Scheduler:  scheduler,
	})
	if err != nil {
		t.Fatalf("NewCodexResponses() error = %v", err)
	}
	return prov
}
