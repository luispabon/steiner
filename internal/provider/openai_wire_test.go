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
