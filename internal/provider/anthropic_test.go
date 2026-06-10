package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestAnthropicRequestMarshalJSON_MapsSystemUserAssistantToolResultAndTools(t *testing.T) {
	wire := anthropicRequestWire(ChatRequest{
		Model:     "claude-3-7-sonnet",
		MaxTokens: intPtr(256),
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "system one"},
			{Role: MessageRoleSystem, Content: "system two"},
			{Role: MessageRoleUser, Content: "hello"},
			{
				Role:             MessageRoleAssistant,
				ReasoningContent: "reason first",
				Content:          "answer",
				ProviderMetadata: &MessageProviderMetadata{
					Anthropic: &AnthropicMessageMetadata{ThinkingSignature: "sig_123"},
				},
				ToolCalls: []ToolCall{{
					ID:        "toolu_1",
					Name:      "lookup",
					Arguments: map[string]any{"query": "weather"},
				}},
			},
			{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: `{"temp":"72F"}`},
		},
		Tools: []ToolSpec{{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:        "lookup",
				Description: "Look up data",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		Params:      map[string]any{"temperature": 0.2},
		ExtraParams: map[string]any{"temperature": 0.8, "top_p": 0.9},
	}, "default-model", true)

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if got["model"] != "claude-3-7-sonnet" {
		t.Fatalf("model = %v, want claude-3-7-sonnet", got["model"])
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
	if got["max_tokens"] != float64(256) {
		t.Fatalf("max_tokens = %v, want 256", got["max_tokens"])
	}
	if got["temperature"] != 0.8 {
		t.Fatalf("temperature = %v, want 0.8 from extra params", got["temperature"])
	}
	if got["top_p"] != 0.9 {
		t.Fatalf("top_p = %v, want 0.9", got["top_p"])
	}

	system, ok := got["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want two blocks", got["system"])
	}

	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v, want three messages", got["messages"])
	}

	assistant, ok := messages[1].(map[string]any)
	if !ok {
		t.Fatalf("assistant message type = %T, want map[string]any", messages[1])
	}
	content, ok := assistant["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("assistant content = %#v, want three blocks", assistant["content"])
	}
	toolUse, ok := content[2].(map[string]any)
	if !ok {
		t.Fatalf("tool use block type = %T, want map[string]any", content[2])
	}
	if toolUse["type"] != "tool_use" {
		t.Fatalf("tool use type = %v, want tool_use", toolUse["type"])
	}
	if toolUse["id"] != "toolu_1" {
		t.Fatalf("tool use id = %v, want toolu_1", toolUse["id"])
	}
	if content[0].(map[string]any)["signature"] != "sig_123" {
		t.Fatalf("thinking signature = %v, want sig_123", content[0].(map[string]any)["signature"])
	}

	toolResult, ok := messages[2].(map[string]any)
	if !ok {
		t.Fatalf("tool result message type = %T, want map[string]any", messages[2])
	}
	toolResultContent, ok := toolResult["content"].([]any)
	if !ok || len(toolResultContent) != 1 {
		t.Fatalf("tool result content = %#v, want one block", toolResult["content"])
	}
	block, ok := toolResultContent[0].(map[string]any)
	if !ok {
		t.Fatalf("tool result block type = %T, want map[string]any", toolResultContent[0])
	}
	if block["type"] != "tool_result" {
		t.Fatalf("tool result type = %v, want tool_result", block["type"])
	}
	if block["tool_use_id"] != "toolu_1" {
		t.Fatalf("tool_use_id = %v, want toolu_1", block["tool_use_id"])
	}

	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", got["tools"])
	}
}

func TestAnthropicRequestWire_DefaultsMaxTokensWhenNil(t *testing.T) {
	wire := anthropicRequestWire(ChatRequest{
		Model:     "claude-3-7-sonnet",
		MaxTokens: nil,
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
	}, "default-model", true)

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if got["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens = %v, want 4096", got["max_tokens"])
	}
}

func TestNormalizeAnthropicChatResponse_MapsTextThinkingToolUseAndUsage(t *testing.T) {
	response, err := normalizeAnthropicChatResponse(&anthropicResponse{
		Role: "assistant",
		Content: []anthropicContentBlock{
			{Type: "thinking", Thinking: "reasoning", Signature: "sig_123"},
			{Type: "text", Text: "final answer"},
			{Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: map[string]any{"query": "weather"}},
		},
		StopReason: "tool_use",
		Usage: &anthropicUsage{
			InputTokens:  11,
			OutputTokens: 7,
		},
	})
	if err != nil {
		t.Fatalf("normalizeAnthropicChatResponse() error = %v", err)
	}

	if got, want := response.Message.Role, MessageRoleAssistant; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}
	if got, want := response.Message.ReasoningContent, "reasoning"; got != want {
		t.Fatalf("reasoning = %q, want %q", got, want)
	}
	if response.Message.ProviderMetadata == nil || response.Message.ProviderMetadata.Anthropic == nil {
		t.Fatalf("provider metadata = %#v, want anthropic metadata", response.Message.ProviderMetadata)
	}
	if got, want := response.Message.ProviderMetadata.Anthropic.ThinkingSignature, "sig_123"; got != want {
		t.Fatalf("thinking signature = %q, want %q", got, want)
	}
	if got, want := response.Message.Content, "final answer"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want one call", response.Message.ToolCalls)
	}
	if got, want := response.Message.ToolCalls[0].ID, "toolu_1"; got != want {
		t.Fatalf("tool call id = %q, want %q", got, want)
	}
	if got, want := response.FinishReason, "tool_calls"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if response.Usage == nil {
		t.Fatal("usage = nil, want usage stats")
	}
	if got, want := response.Usage.PromptTokens, 11; got != want {
		t.Fatalf("prompt tokens = %d, want %d", got, want)
	}
	if got, want := response.Usage.CompletionTokens, 7; got != want {
		t.Fatalf("completion tokens = %d, want %d", got, want)
	}
	if got, want := response.Usage.TotalTokens, 18; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
}

func TestAnthropicBuildHTTPRequest(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	p := &Anthropic{
		OpenAICompat: &OpenAICompat{
			baseURL: parsed,
			apiKey:  "test-key",
		},
	}

	req, err := p.buildHTTPRequest(context.Background(), []byte(`{"model":"claude-3-7-sonnet"}`), false)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
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

func TestAnthropicBuildHTTPRequest_Stream(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	p := &Anthropic{
		OpenAICompat: &OpenAICompat{
			baseURL: parsed,
			apiKey:  "test-key",
		},
	}

	req, err := p.buildHTTPRequest(context.Background(), []byte(`{"model":"claude-3-7-sonnet"}`), true)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
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

func TestAnthropicBuildHTTPRequest_NoAuth(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	p := &Anthropic{
		OpenAICompat: &OpenAICompat{
			baseURL: parsed,
			apiKey:  "",
		},
	}

	req, err := p.buildHTTPRequest(context.Background(), []byte(`{"model":"claude-3-7-sonnet"}`), false)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
}

func TestAnthropicBuildHTTPRequest_HeaderOverrides(t *testing.T) {
	parsed, err := url.Parse("http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	p := &Anthropic{
		OpenAICompat: &OpenAICompat{
			baseURL: parsed,
			apiKey:  "test-key",
			headers: map[string]string{
				"x-api-key":         "override-key",
				"anthropic-version": "2024-01-01",
				"Authorization":     "Manual test auth",
				"Content-Type":      "application/custom+json",
				"Accept":            "application/custom-stream",
			},
		},
	}

	req, err := p.buildHTTPRequest(context.Background(), []byte(`{"model":"claude-3-7-sonnet"}`), true)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
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
