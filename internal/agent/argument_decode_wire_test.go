package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// wireCase is one provider family answering a malformed tool call. first and
// second are the response bodies for the first and second model requests.
type wireCase struct {
	newProvider func(baseURL string) (provider.Provider, error)
	streaming   bool
	contentType string
	first       string
	second      string
}

// TestMalformedToolCallOverWire runs a model response with malformed tool
// arguments through each provider wire. The executor must reject the call, and
// the next encoded request must replay the call with {} arguments and a
// matching invalid_tool_input result.
func TestMalformedToolCallOverWire(t *testing.T) {
	t.Run("openai_chat", func(t *testing.T) {
		body := runMalformedCallOverWire(t, wireCase{
			newProvider: func(baseURL string) (provider.Provider, error) {
				return provider.NewOpenAICompat(provider.ClientConfig{BaseURL: baseURL + "/v1", Model: "wire-test-model"})
			},
			contentType: "application/json",
			first:       `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_bad","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":"tool_calls"}]}`,
			second:      `{"id":"c2","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`,
		})
		doc := decodeWireBody(t, body)
		call := findJSONObject(doc, func(m map[string]any) bool { return m["id"] == "call_bad" && m["function"] != nil })
		if call == nil {
			t.Fatal("second request has no assistant tool call call_bad")
		}
		if fn, _ := call["function"].(map[string]any); fn["arguments"] != "{}" {
			t.Errorf("assistant call arguments = %v, want \"{}\"", fn["arguments"])
		}
		result := findJSONObject(doc, func(m map[string]any) bool { return m["tool_call_id"] == "call_bad" })
		assertInvalidInputResult(t, result, "content")
	})

	t.Run("anthropic", func(t *testing.T) {
		body := runMalformedCallOverWire(t, wireCase{
			newProvider: func(baseURL string) (provider.Provider, error) {
				return provider.NewAnthropic(provider.ClientConfig{BaseURL: baseURL, APIKey: "test-key", Model: "wire-test-model"})
			},
			streaming:   true,
			contentType: "text/event-stream",
			first: sseBody(
				"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"wire-test-model","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_bad","name":"read","input":{}}}`,
				"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
				"content_block_stop", `{"type":"content_block_stop","index":0}`,
				"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
				"message_stop", `{"type":"message_stop"}`,
			),
			second: sseBody(
				"message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"wire-test-model","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
				"content_block_stop", `{"type":"content_block_stop","index":0}`,
				"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
				"message_stop", `{"type":"message_stop"}`,
			),
		})
		doc := decodeWireBody(t, body)
		call := findJSONObject(doc, func(m map[string]any) bool { return m["type"] == "tool_use" && m["id"] == "toolu_bad" })
		if call == nil {
			t.Fatal("second request has no tool_use toolu_bad")
		}
		if input, _ := call["input"].(map[string]any); input == nil || len(input) != 0 {
			t.Errorf("tool_use input = %v, want {}", call["input"])
		}
		result := findJSONObject(doc, func(m map[string]any) bool { return m["type"] == "tool_result" && m["tool_use_id"] == "toolu_bad" })
		assertInvalidInputResult(t, result, "content")
	})

	t.Run("codex_responses", func(t *testing.T) {
		body := runMalformedCallOverWire(t, wireCase{
			newProvider: func(baseURL string) (provider.Provider, error) {
				return provider.NewCodexResponses(provider.ClientConfig{BaseURL: baseURL, APIKey: "test-key", Model: "wire-test-model"})
			},
			contentType: "application/json",
			first:       `{"id":"r1","object":"response","status":"completed","output":[{"type":"function_call","call_id":"call_bad","name":"read","arguments":"{\"path\":"}]}`,
			second:      `{"id":"r2","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`,
		})
		doc := decodeWireBody(t, body)
		call := findJSONObject(doc, func(m map[string]any) bool { return m["type"] == "function_call" && m["call_id"] == "call_bad" })
		if call == nil {
			t.Fatal("second request has no function_call call_bad")
		}
		if call["arguments"] != "{}" {
			t.Errorf("function_call arguments = %v, want \"{}\"", call["arguments"])
		}
		result := findJSONObject(doc, func(m map[string]any) bool { return m["type"] == "function_call_output" && m["call_id"] == "call_bad" })
		assertInvalidInputResult(t, result, "output")
	})
}

// runMalformedCallOverWire runs one agent turn against a fake provider server
// that answers the first request with wc.first and the second with wc.second,
// and returns the body of the second request.
func runMalformedCallOverWire(t *testing.T, wc wireCase) []byte {
	t.Helper()
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		answer := wc.first
		if len(bodies) > 1 {
			answer = wc.second
		}
		mu.Unlock()
		w.Header().Set("Content-Type", wc.contentType)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(server.Close)

	prov, err := wc.newProvider(server.URL)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	executor := tool.NewExecutor(tool.NewRegistry(tool.ToolDef{
		Name: "read",
		Handler: func(context.Context, map[string]any) (any, error) {
			return "hello", nil
		},
	}), config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{})

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:           prov,
		Executor:           executor,
		Tools:              []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Description: "Read files", Parameters: map[string]any{"type": "object"}}}},
		Prompt:             prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "read it"}}, ProjectContextBudgetBytes: 128},
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "wire-test-model"},
		MaxTokens:          intPtr(64),
		Limits:             Limits{MaxTurns: 4, MaxTokens: 50},
		StreamingPreferred: wc.streaming,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("model requests = %d, want 2", len(bodies))
	}
	return bodies[1]
}

// sseBody encodes alternating event names and JSON data as a server-sent
// events stream.
func sseBody(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", pairs[i], pairs[i+1])
	}
	return b.String()
}

func decodeWireBody(t *testing.T, body []byte) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode request body: %v\n%s", err, body)
	}
	return doc
}

// findJSONObject returns the first JSON object in doc, in document order, for
// which match returns true, or nil.
func findJSONObject(doc any, match func(map[string]any) bool) map[string]any {
	switch node := doc.(type) {
	case map[string]any:
		if match(node) {
			return node
		}
		for _, child := range node {
			if found := findJSONObject(child, match); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range node {
			if found := findJSONObject(child, match); found != nil {
				return found
			}
		}
	}
	return nil
}

// assertInvalidInputResult fails unless result exists and its field holds an
// invalid_tool_input envelope.
func assertInvalidInputResult(t *testing.T, result map[string]any, field string) {
	t.Helper()
	if result == nil {
		t.Fatal("second request has no tool result for call_bad")
	}
	if text := fmt.Sprint(result[field]); !strings.Contains(text, `"kind":"invalid_tool_input"`) {
		t.Errorf("tool result %s = %s, want invalid_tool_input envelope", field, text)
	}
}
