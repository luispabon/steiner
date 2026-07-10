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
