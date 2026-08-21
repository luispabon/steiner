package provider

import (
	"encoding/json"
	"testing"
)

func TestChatRequestWire_IncludeEmptyReasoning_FillsAssistantMessages(t *testing.T) {
	tests := []struct {
		name                  string
		messages              []Message
		includeEmptyReasoning bool
		wantReasoningNil      []bool // per-message: true = nil expected, false = non-nil expected
	}{
		{
			name: "fills nil reasoning on assistant messages when flag set",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hi"},
				{Role: MessageRoleAssistant, Content: "hello"},
				{Role: MessageRoleAssistant, Content: "world", ReasoningContent: "thinking"},
			},
			includeEmptyReasoning: true,
			wantReasoningNil:      []bool{true, false, false},
		},
		{
			name: "leaves messages unchanged when flag not set",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hi"},
				{Role: MessageRoleAssistant, Content: "hello"},
			},
			includeEmptyReasoning: false,
			wantReasoningNil:      []bool{true, true},
		},
		{
			name: "does not fill reasoning on user or tool messages",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hi"},
				{Role: MessageRoleTool, Content: "result", ToolCallID: "id1"},
				{Role: MessageRoleAssistant, Content: "ok"},
			},
			includeEmptyReasoning: true,
			wantReasoningNil:      []bool{true, true, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{
				Model:                 "test-model",
				Messages:              tt.messages,
				IncludeEmptyReasoning: tt.includeEmptyReasoning,
			}
			wire, err := chatRequestWire(req, "test-model", false)
			if err != nil {
				t.Fatalf("chatRequestWire() error = %v", err)
			}
			if len(wire.Messages) != len(tt.wantReasoningNil) {
				t.Fatalf("got %d messages, want %d", len(wire.Messages), len(tt.wantReasoningNil))
			}
			for i, wantNil := range tt.wantReasoningNil {
				gotNil := wire.Messages[i].ReasoningContent == nil
				if gotNil != wantNil {
					t.Errorf("message[%d] ReasoningContent nil = %v, want %v", i, gotNil, wantNil)
				}
			}
		})
	}
}

func TestChatRequestWire_IncludeEmptyReasoning_EmptyStringValue(t *testing.T) {
	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: MessageRoleAssistant, Content: "answer"},
		},
		IncludeEmptyReasoning: true,
	}
	wire, err := chatRequestWire(req, "test-model", false)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	if wire.Messages[0].ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want pointer to empty string")
	}
	if *wire.Messages[0].ReasoningContent != "" {
		t.Fatalf("*ReasoningContent = %q, want empty string", *wire.Messages[0].ReasoningContent)
	}
}

func TestChatRequestWire_IncludeEmptyReasoning_PreservesExistingReasoning(t *testing.T) {
	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: MessageRoleAssistant, Content: "answer", ReasoningContent: "deep thought"},
		},
		IncludeEmptyReasoning: true,
	}
	wire, err := chatRequestWire(req, "test-model", false)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	if wire.Messages[0].ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want non-nil")
	}
	if *wire.Messages[0].ReasoningContent != "deep thought" {
		t.Fatalf("*ReasoningContent = %q, want %q", *wire.Messages[0].ReasoningContent, "deep thought")
	}
}

func TestBuildRequestPayload_PreservesEmptyNestedToolSchemaMaps(t *testing.T) {
	tools := CloneTools([]ToolSpec{
		{
			Type: "function",
			Function: ToolFunctionSpec{
				Name: "advisor",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties":           map[string]any{},
				},
			},
		},
	})

	w := testOpenAIWire(t, "http://localhost:11434/v1", "gpt-4")
	req := ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		Tools:    tools,
	}

	data, err := w.Payload(req, false)
	if err != nil {
		t.Fatalf("Payload() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	rawTools, ok := payload["tools"].([]any)
	if !ok {
		t.Fatal("tools missing from payload")
	}
	if len(rawTools) != 1 {
		t.Fatalf("tools length = %d, want 1", len(rawTools))
	}

	tool, ok := rawTools[0].(map[string]any)
	if !ok {
		t.Fatal("tool is not a map")
	}
	function, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatal("function is not a map")
	}
	parameters, ok := function["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters = %#v, want map", function["parameters"])
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want empty map", parameters["properties"])
	}
	if properties == nil {
		t.Fatal("properties = nil, want empty map")
	}
	if len(properties) != 0 {
		t.Fatalf("len(properties) = %d, want 0", len(properties))
	}
}

func TestUsageStatsNonCachedPromptTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage UsageStats
		want  int
	}{
		{name: "zero value", want: 0},
		{name: "positive non-cached remainder", usage: UsageStats{PromptTokens: 100, CacheReadInputTokens: 30, CacheCreationInputTokens: 20}, want: 50},
		{name: "negative remainder clamps to zero", usage: UsageStats{PromptTokens: 10, CacheReadInputTokens: 8, CacheCreationInputTokens: 5}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.NonCachedPromptTokens(); got != tt.want {
				t.Fatalf("NonCachedPromptTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}
