package provider

import (
	"encoding/json"
	"testing"
)

func TestResponsesRequestMarshalJSONIncludesPromptCacheFields(t *testing.T) {
	req := responsesRequest{
		Model:          "gpt-5.5",
		Input:          []responsesItem{{Type: "message", Role: "user"}},
		PromptCacheKey: "session-123",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := payload["prompt_cache_key"], "session-123"; got != want {
		t.Fatalf("prompt_cache_key = %v, want %v", got, want)
	}
	if _, present := payload["prompt_cache_retention"]; present {
		t.Fatalf("prompt_cache_retention must not be sent to Codex backend (unsupported, causes 400)")
	}
}

func TestResponsesRequestMarshalJSONOmitsReasoningWhenNil(t *testing.T) {
	req := responsesRequest{
		Model: "gpt-5.5",
		Input: []responsesItem{{Type: "message", Role: "user"}},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatal("reasoning present in JSON, want absent when Reasoning is nil")
	}
}

func TestResponsesRequestMarshalJSONIncludesReasoningEffort(t *testing.T) {
	req := responsesRequest{
		Model:     "gpt-5.5",
		Input:     []responsesItem{{Type: "message", Role: "user"}},
		Reasoning: &ReasoningRequest{Effort: "low"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want map[string]any", payload["reasoning"])
	}
	if got, want := reasoning["effort"], "low"; got != want {
		t.Fatalf("reasoning.effort = %v, want %v", got, want)
	}
	if got, want := reasoning["summary"], "auto"; got != want {
		t.Fatalf("reasoning.summary = %v, want %v", got, want)
	}
}

func TestResponsesRequestMarshalJSONReasoningOverridesParamsAndExtraParams(t *testing.T) {
	req := responsesRequest{
		Model:     "gpt-5.5",
		Input:     []responsesItem{{Type: "message", Role: "user"}},
		Reasoning: &ReasoningRequest{Effort: "high"},
		Params: map[string]any{
			"reasoning": map[string]any{"effort": "low"},
		},
		ExtraParams: map[string]any{
			"reasoning": map[string]any{"effort": "medium"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want map[string]any", payload["reasoning"])
	}
	if got, want := reasoning["effort"], "high"; got != want {
		t.Fatalf("reasoning.effort = %v, want %v (first-class field must win)", got, want)
	}
	if got, want := reasoning["summary"], "auto"; got != want {
		t.Fatalf("reasoning.summary = %v, want %v", got, want)
	}
}

func TestResponsesRequestWire_ThreadsReasoning(t *testing.T) {
	tests := []struct {
		name      string
		reasoning *ReasoningRequest
	}{
		{name: "nil reasoning", reasoning: nil},
		{name: "explicit effort", reasoning: &ReasoningRequest{Effort: "medium"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{
				Model:     "test-model",
				Messages:  []Message{{Role: MessageRoleUser, Content: "hi"}},
				Reasoning: tt.reasoning,
			}
			wire, err := responsesRequestWire(req, "test-model", false)
			if err != nil {
				t.Fatalf("responsesRequestWire() error = %v", err)
			}
			if wire.Reasoning != tt.reasoning {
				t.Fatalf("wire.Reasoning = %v, want %v", wire.Reasoning, tt.reasoning)
			}
		})
	}
}

func TestResponsesRequestMarshalJSONOmitsMaxOutputTokens(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens *int
	}{
		{
			name:      "omits max_output_tokens when set",
			maxTokens: intPtr(256),
		},
		{
			name:      "omits max_output_tokens when nil",
			maxTokens: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{
				Model:     "test-model",
				Messages:  []Message{{Role: MessageRoleUser, Content: "hi"}},
				MaxTokens: tt.maxTokens,
			}
			wire, err := responsesRequestWire(req, "test-model", true)
			if err != nil {
				t.Fatalf("responsesRequestWire() error = %v", err)
			}

			data, err := json.Marshal(wire)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			if _, present := payload["max_output_tokens"]; present {
				t.Fatal("max_output_tokens present in JSON, want absent (Codex Responses API does not support it)")
			}
		})
	}
}

func TestNormalizeResponsesResponseReasoning(t *testing.T) {
	payload := responsesResponse{
		Output: []responsesItem{
			{
				Type: "reasoning",
				ID:   "rs_123",
				Summary: []responsesContentPart{
					{Type: "summary_text", Text: "thinking about it"},
				},
			},
			{
				Type:    "message",
				Content: []responsesContentPart{{Type: "output_text", Text: "final answer"}},
			},
		},
	}

	resp, err := normalizeResponsesResponse(payload)
	if err != nil {
		t.Fatalf("normalizeResponsesResponse() error = %v", err)
	}
	if got, want := resp.Message.ReasoningContent, "thinking about it"; got != want {
		t.Fatalf("ReasoningContent = %q, want %q", got, want)
	}
	if resp.Message.ProviderMetadata == nil || resp.Message.ProviderMetadata.Codex == nil {
		t.Fatal("ProviderMetadata.Codex = nil, want set")
	}
	if got, want := resp.Message.ProviderMetadata.Codex.ReasoningID, "rs_123"; got != want {
		t.Fatalf("ReasoningID = %q, want %q", got, want)
	}
	if got, want := resp.Message.Content, "final answer"; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
}

func TestNormalizeResponsesResponseNoReasoning(t *testing.T) {
	payload := responsesResponse{
		Output: []responsesItem{
			{
				Type:    "message",
				Content: []responsesContentPart{{Type: "output_text", Text: "final answer"}},
			},
			{
				Type:   "function_call",
				CallID: "call_1",
				Name:   "tool",
				Args:   "{}",
			},
		},
	}

	resp, err := normalizeResponsesResponse(payload)
	if err != nil {
		t.Fatalf("normalizeResponsesResponse() error = %v", err)
	}
	if resp.Message.ReasoningContent != "" {
		t.Fatalf("ReasoningContent = %q, want empty", resp.Message.ReasoningContent)
	}
	if resp.Message.ProviderMetadata != nil {
		t.Fatalf("ProviderMetadata = %v, want nil", resp.Message.ProviderMetadata)
	}
}

func TestMessageToResponsesItemsWithReasoning(t *testing.T) {
	msg := Message{
		Role:             MessageRoleAssistant,
		Content:          "final answer",
		ReasoningContent: "thinking about it",
		ProviderMetadata: &MessageProviderMetadata{
			Codex: &CodexMessageMetadata{ReasoningID: "rs_123"},
		},
	}

	items, err := messageToResponsesItems(msg)
	if err != nil {
		t.Fatalf("messageToResponsesItems() error = %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("len(items) = %d, want >= 2", len(items))
	}
	reasoningItem := items[0]
	if got, want := reasoningItem.Type, "reasoning"; got != want {
		t.Fatalf("items[0].Type = %q, want %q", got, want)
	}
	if got, want := reasoningItem.ID, "rs_123"; got != want {
		t.Fatalf("items[0].ID = %q, want %q", got, want)
	}
	if len(reasoningItem.Summary) != 1 || reasoningItem.Summary[0].Type != "summary_text" || reasoningItem.Summary[0].Text != "thinking about it" {
		t.Fatalf("items[0].Summary = %+v, want single summary_text part", reasoningItem.Summary)
	}
	if got, want := items[1].Type, "message"; got != want {
		t.Fatalf("items[1].Type = %q, want %q", got, want)
	}
}

func TestMessageToResponsesItemsWithReasoningAndToolCalls(t *testing.T) {
	msg := Message{
		Role:             MessageRoleAssistant,
		ReasoningContent: "thinking about it",
		ProviderMetadata: &MessageProviderMetadata{
			Codex: &CodexMessageMetadata{ReasoningID: "rs_123"},
		},
		ToolCalls: []ToolCall{
			{ID: "call_1", Name: "tool", RawArguments: "{}"},
		},
	}

	items, err := messageToResponsesItems(msg)
	if err != nil {
		t.Fatalf("messageToResponsesItems() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if got, want := items[0].Type, "reasoning"; got != want {
		t.Fatalf("items[0].Type = %q, want %q", got, want)
	}
	if got, want := items[1].Type, "function_call"; got != want {
		t.Fatalf("items[1].Type = %q, want %q", got, want)
	}
}

func TestMessageToResponsesItemsWithoutReasoning(t *testing.T) {
	msg := Message{
		Role:    MessageRoleAssistant,
		Content: "final answer",
	}

	items, err := messageToResponsesItems(msg)
	if err != nil {
		t.Fatalf("messageToResponsesItems() error = %v", err)
	}
	for _, item := range items {
		if item.Type == "reasoning" {
			t.Fatalf("unexpected reasoning item: %+v", item)
		}
	}
}

func TestReasoningWirePayload(t *testing.T) {
	reasoning := &ReasoningRequest{Effort: "medium"}
	payload := reasoningWirePayload(reasoning)

	if got, want := payload["effort"], "medium"; got != want {
		t.Fatalf("payload[\"effort\"] = %v, want %v", got, want)
	}
	if got, want := payload["summary"], "auto"; got != want {
		t.Fatalf("payload[\"summary\"] = %v, want %v", got, want)
	}
}
