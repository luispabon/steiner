package provider

import (
	"encoding/json"
	"testing"
)

func intPtr(v int) *int { return &v }

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

func TestOpenAIRequestMarshalJSONPreservesThinkingDisabledExtraParams(t *testing.T) {
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
