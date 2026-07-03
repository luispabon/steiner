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
