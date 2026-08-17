package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatChatCompletionNormalizesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, http.MethodPost; got != want {
			t.Fatalf("method = %s, want %s", got, want)
		}
		if got, want := r.URL.Path, "/v1/chat/completions"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		if got, want := r.Header.Get("Content-Type"), "application/json"; got != want {
			t.Fatalf("content type = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), ""; got != want {
			t.Fatalf("authorization = %q, want empty", got)
		}
		_, _ = fmt.Fprint(w, `{
			"choices":[
				{
					"message":{
						"role":"assistant",
						"content":"final text",
						"tool_calls":[
							{
								"id":"call_1",
								"type":"function",
								"function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}
							}
						]
					},
					"finish_reason":"stop"
				}
			],
			"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}
		}`)
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got, want := resp.Message.Role, MessageRoleAssistant; got != want {
		t.Fatalf("role = %s, want %s", got, want)
	}
	if got, want := resp.Message.Content, "final text"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got, want := resp.FinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if resp.Usage == nil {
		t.Fatal("usage = nil, want usage")
	}
	if got, want := resp.Usage.TotalTokens, 8; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(resp.Message.ToolCalls))
	}
	if got, want := resp.Message.ToolCalls[0].Name, "lookup"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
	if got, want := resp.Message.ToolCalls[0].Arguments["query"], "x"; got != want {
		t.Fatalf("tool call query = %#v, want %#v", got, want)
	}
}

func TestOpenAICompatStreamChatCompletionNormalizesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/chat/completions"; got != want {
			t.Fatalf("path = %s, want %s", got, want)
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not implement Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"role":"assistant"}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hello "}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":"}}]}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", "[DONE]")
		flusher.Flush()
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}

	first, ok := <-ch
	if !ok {
		t.Fatal("stream closed before content chunk")
	}
	if got, want := first.Delta.Role, MessageRoleAssistant; got != want {
		t.Fatalf("first chunk role = %s, want %s", got, want)
	}
	if got, want := first.Delta.Content, "hello "; got != want {
		t.Fatalf("first chunk content = %q, want %q", got, want)
	}

	second, ok := <-ch
	if !ok {
		t.Fatal("stream closed before final chunk")
	}
	if !second.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if got, want := second.FinishReason, "tool_calls"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if second.Usage == nil {
		t.Fatal("usage = nil, want usage")
	}
	if got, want := second.Usage.TotalTokens, 6; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
	if len(second.Delta.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(second.Delta.ToolCalls))
	}
	if got, want := second.Delta.ToolCalls[0].Name, "lookup"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
	if got, want := second.Delta.ToolCalls[0].Arguments["query"], "x"; got != want {
		t.Fatalf("tool call query = %#v, want %#v", got, want)
	}
	if _, ok := <-ch; ok {
		t.Fatal("stream produced unexpected extra chunk")
	}
}

func TestOpenAICompatStreamChatCompletionReportsStreamErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, "bad upstream")
	}))
	defer server.Close()

	provider := mustTestOpenAICompat(t, server.URL)

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v, want channel", err)
	}

	chunk, ok := <-ch
	if !ok {
		t.Fatal("stream closed before error chunk")
	}
	if !chunk.Done {
		t.Fatal("error chunk Done = false, want true")
	}
	if chunk.Error == "" {
		t.Fatal("error chunk Error = empty, want upstream error")
	}
	if _, ok := <-ch; ok {
		t.Fatal("stream produced unexpected extra chunk after error")
	}
}
