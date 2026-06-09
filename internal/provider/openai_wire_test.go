package provider

import (
	"encoding/json"
	"testing"
)

func intPtr(v int) *int { return &v }

func TestToOpenAIMessage_SetsReasoningContentPointerWhenPresent(t *testing.T) {
	msg := Message{
		Role:             MessageRoleAssistant,
		Content:          "answer",
		ReasoningContent: "my reasoning",
	}
	wire, err := toOpenAIMessage(msg)
	if err != nil {
		t.Fatalf("toOpenAIMessage() error = %v", err)
	}
	if wire.ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want non-nil pointer")
	}
	if *wire.ReasoningContent != "my reasoning" {
		t.Fatalf("*ReasoningContent = %q, want %q", *wire.ReasoningContent, "my reasoning")
	}
}

func TestToOpenAIMessage_SetsReasoningContentNilWhenAbsent(t *testing.T) {
	msg := Message{
		Role:    MessageRoleAssistant,
		Content: "answer",
	}
	wire, err := toOpenAIMessage(msg)
	if err != nil {
		t.Fatalf("toOpenAIMessage() error = %v", err)
	}
	if wire.ReasoningContent != nil {
		t.Fatalf("ReasoningContent = %v, want nil", *wire.ReasoningContent)
	}
}

func TestNormalizeMessage_DereferencesReasoningContentPointer(t *testing.T) {
	reasoning := "step by step"
	wire := openAIMessage{
		Role:             "assistant",
		Content:          "done",
		ReasoningContent: &reasoning,
	}
	out, err := normalizeMessage(wire)
	if err != nil {
		t.Fatalf("normalizeMessage() error = %v", err)
	}
	if out.ReasoningContent != "step by step" {
		t.Fatalf("ReasoningContent = %q, want %q", out.ReasoningContent, "step by step")
	}
}

func TestNormalizeMessage_ReasoningContentNilProducesEmptyString(t *testing.T) {
	wire := openAIMessage{
		Role:    "assistant",
		Content: "done",
	}
	out, err := normalizeMessage(wire)
	if err != nil {
		t.Fatalf("normalizeMessage() error = %v", err)
	}
	if out.ReasoningContent != "" {
		t.Fatalf("ReasoningContent = %q, want empty string", out.ReasoningContent)
	}
}

func TestNormalizeMessage_UsesReasoningDetailsText(t *testing.T) {
	wire := openAIMessage{
		Role:    "assistant",
		Content: "done",
		ReasoningDetails: []any{
			map[string]any{"type": "text", "text": "first "},
			map[string]any{"type": "summary_text", "text": map[string]any{"value": "second"}},
			map[string]any{"type": "signature", "signature": "ignored"},
		},
	}

	out, err := normalizeMessage(wire)
	if err != nil {
		t.Fatalf("normalizeMessage() error = %v", err)
	}
	if out.ReasoningContent != "first second" {
		t.Fatalf("ReasoningContent = %q, want %q", out.ReasoningContent, "first second")
	}
}

func TestNormalizeMessage_AppendsReasoningDetailsToReasoningContent(t *testing.T) {
	reasoning := "prefix "
	wire := openAIMessage{
		Role:             "assistant",
		Content:          "done",
		ReasoningContent: &reasoning,
		ReasoningDetails: []any{
			map[string]any{"type": "text", "text": "suffix"},
		},
	}

	out, err := normalizeMessage(wire)
	if err != nil {
		t.Fatalf("normalizeMessage() error = %v", err)
	}
	if out.ReasoningContent != "prefix suffix" {
		t.Fatalf("ReasoningContent = %q, want %q", out.ReasoningContent, "prefix suffix")
	}
}

func TestNormalizeMessage_PreservesToolCallsWithEmptyOrWhitespaceContent(t *testing.T) {
	tests := []struct {
		name         string
		content      any
		wantContent  string
		wantArgValue string
	}{
		{
			name:         "empty content",
			content:      "",
			wantArgValue: "test",
		},
		{
			name:         "whitespace content",
			content:      "   ",
			wantContent:  "   ",
			wantArgValue: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := openAIMessage{
				Role:    "assistant",
				Content: tt.content,
				ToolCalls: []openAIToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: openAIToolCallFunction{
						Name:      "lookup",
						Arguments: `{"query":"test"}`,
					},
				}},
			}

			out, err := normalizeMessage(wire)
			if err != nil {
				t.Fatalf("normalizeMessage() error = %v", err)
			}
			if out.Content != tt.wantContent {
				t.Fatalf("Content = %q, want %q", out.Content, tt.wantContent)
			}
			if len(out.ToolCalls) != 1 {
				t.Fatalf("ToolCalls len = %d, want 1", len(out.ToolCalls))
			}
			if got := out.ToolCalls[0].Arguments["query"]; got != tt.wantArgValue {
				t.Fatalf("tool call query = %v, want %q", got, tt.wantArgValue)
			}
		})
	}
}

func TestOpenAIMessageMarshalJSON_NilReasoningContentOmitted(t *testing.T) {
	msg := openAIMessage{
		Role:    "assistant",
		Content: "hello",
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := m["reasoning_content"]; ok {
		t.Fatal("reasoning_content present in JSON, want absent when nil")
	}
}

func TestOpenAIMessageMarshalJSON_EmptyStringReasoningContentPresent(t *testing.T) {
	empty := ""
	msg := openAIMessage{
		Role:             "assistant",
		Content:          "hello",
		ReasoningContent: &empty,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	val, ok := m["reasoning_content"]
	if !ok {
		t.Fatal("reasoning_content absent from JSON, want present as empty string")
	}
	if val != "" {
		t.Fatalf("reasoning_content = %v, want empty string", val)
	}
}

func TestOpenAIRequestMarshalJSONFlattensExtraParams(t *testing.T) {
	req := openAIRequest{
		Model:    "gpt-4",
		Messages: []openAIMessage{{Role: "user", Content: "hello"}},
		ExtraParams: map[string]any{
			"temperature": 0.7,
			"top_p":       0.9,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if got, want := m["model"], "gpt-4"; got != want {
		t.Fatalf("model = %v, want %v", got, want)
	}
	if got, want := m["temperature"], 0.7; got != want {
		t.Fatalf("temperature = %v, want %v", got, want)
	}
	if got, want := m["top_p"], 0.9; got != want {
		t.Fatalf("top_p = %v, want %v", got, want)
	}
	if _, ok := m["max_tokens"]; ok {
		t.Fatal("max_tokens should be omitted when unset")
	}
}

func TestOpenAIRequestMarshalJSONExplicitFieldsOverrideExtraParams(t *testing.T) {
	req := openAIRequest{
		Model:     "gpt-4",
		Messages:  []openAIMessage{{Role: "user", Content: "hello"}},
		MaxTokens: intPtr(100),
		Stream:    true,
		ExtraParams: map[string]any{
			"model":      "should-be-overridden",
			"max_tokens": 999,
			"stream":     false,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if got, want := m["model"], "gpt-4"; got != want {
		t.Fatalf("model = %v, want %v (should override extra_params)", got, want)
	}
	if got, want := m["max_tokens"], float64(100); got != want {
		t.Fatalf("max_tokens = %v, want %v (should override extra_params)", got, want)
	}
	if got, want := m["stream"], true; got != want {
		t.Fatalf("stream = %v, want %v (should override extra_params)", got, want)
	}
}

func TestOpenAIRequestMarshalJSONParamsApplied(t *testing.T) {
	// Test: Params are included in the wire JSON
	req := openAIRequest{
		Model:    "gpt-4",
		Messages: []openAIMessage{{Role: "user", Content: "hello"}},
		Params: map[string]any{
			"temperature": 0.8,
			"top_p":       0.95,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if got, want := m["temperature"], 0.8; got != want {
		t.Fatalf("temperature = %v, want %v", got, want)
	}
	if got, want := m["top_p"], 0.95; got != want {
		t.Fatalf("top_p = %v, want %v", got, want)
	}
}

func TestOpenAIRequestMarshalJSONExtraParamsOverrideParams(t *testing.T) {
	// Test: ExtraParams override Params on key collision
	// Merge order: Params < ExtraParams < explicit fields
	req := openAIRequest{
		Model:    "gpt-4",
		Messages: []openAIMessage{{Role: "user", Content: "hello"}},
		Params: map[string]any{
			"temperature": 0.8,
			"top_p":       0.95,
		},
		ExtraParams: map[string]any{
			"temperature":       0.5, // Should override Params value
			"frequency_penalty": 0.1,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if got, want := m["temperature"], 0.5; got != want {
		t.Fatalf("temperature = %v, want %v (extra_params should override params)", got, want)
	}
	if got, want := m["top_p"], 0.95; got != want {
		t.Fatalf("top_p = %v, want %v (from params, unchanged)", got, want)
	}
	if got, want := m["frequency_penalty"], 0.1; got != want {
		t.Fatalf("frequency_penalty = %v, want %v (from extra_params)", got, want)
	}
}

func TestOpenAIRequestMarshalJSONMergeOrderPrecedence(t *testing.T) {
	// Test: Full merge order: Params < ExtraParams < explicit fields
	req := openAIRequest{
		Model:     "gpt-4",
		Messages:  []openAIMessage{{Role: "user", Content: "hello"}},
		MaxTokens: intPtr(200),
		Params: map[string]any{
			"temperature": 0.8,
			"max_tokens":  100, // Will be overridden by explicit field
		},
		ExtraParams: map[string]any{
			"temperature": 0.5, // Overrides Params
			"top_p":       0.9,
			"max_tokens":  150, // Will be overridden by explicit field
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if got, want := m["temperature"], 0.5; got != want {
		t.Fatalf("temperature = %v, want %v (extra_params overrides params)", got, want)
	}
	if got, want := m["top_p"], 0.9; got != want {
		t.Fatalf("top_p = %v, want %v (from extra_params)", got, want)
	}
	if got, want := m["max_tokens"], float64(200); got != want {
		t.Fatalf("max_tokens = %v, want %v (explicit field overrides both)", got, want)
	}
}

func TestOpenAIRequestMarshalJSONPreservesReasoningExtraParams(t *testing.T) {
	req := openAIRequest{
		Model:    "gpt-4",
		Messages: []openAIMessage{{Role: "user", Content: "hello"}},
		ExtraParams: map[string]any{
			"reasoning": map[string]any{
				"enabled": false,
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	reasoningRaw, ok := m["reasoning"]
	if !ok {
		t.Fatal("reasoning missing from request body")
	}
	reasoning, ok := reasoningRaw.(map[string]any)
	if !ok {
		t.Fatalf("reasoning type = %T, want map[string]any", reasoningRaw)
	}
	if got, want := reasoning["enabled"], false; got != want {
		t.Fatalf("reasoning.enabled = %v, want %v", got, want)
	}
}

func TestNormalizeToolCalls_TrailingCommaInArguments(t *testing.T) {
	calls := []openAIToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: openAIToolCallFunction{
				Name:      "mutate",
				Arguments: `{"operations":[{"type":"write","path":"foo.md"},]}`,
			},
		},
	}
	got, err := normalizeToolCalls(calls)
	if err != nil {
		t.Fatalf("expected trailing comma to be sanitized, got error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d calls, want 1", len(got))
	}
	if got[0].Name != "mutate" {
		t.Fatalf("Name = %q, want %q", got[0].Name, "mutate")
	}
}

func TestNormalizeToolCalls_TrailingCommaInNestedObject(t *testing.T) {
	calls := []openAIToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: openAIToolCallFunction{
				Name:      "mutate",
				Arguments: `{"operations":[{"type":"write","path":"a.go","content":"x",},]}`,
			},
		},
	}
	got, err := normalizeToolCalls(calls)
	if err != nil {
		t.Fatalf("expected nested trailing commas to be sanitized, got error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d calls, want 1", len(got))
	}
}
