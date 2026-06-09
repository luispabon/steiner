package provider

import (
	"encoding/json"
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

func TestNormalizeAnthropicChatResponse_MapsTextThinkingToolUseAndUsage(t *testing.T) {
	response, err := normalizeAnthropicChatResponse(&anthropicResponse{
		Role: "assistant",
		Content: []anthropicContentBlock{
			{Type: "thinking", Thinking: "reasoning"},
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
