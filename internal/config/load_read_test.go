package config

import "testing"

func TestParseConfigPatchPreservesParamsAndExtraParams(t *testing.T) {
	patch, err := parseConfigPatch("test.yaml", `models:
  default:
    provider: local
    id: qwen3
    params:
      temperature: 0.4
      top_p: 0.95
    extra_params:
      reasoning:
        enabled: false
      metadata:
        tier: offline
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.Models == nil {
		t.Fatal("patch.Models = nil, want parsed model patch")
	}

	model, ok := (*patch.Models)["default"]
	if !ok {
		t.Fatal("patch.Models[default] missing")
	}
	if model.Params == nil {
		t.Fatal("model.Params = nil, want parsed params")
	}
	if got, want := (*model.Params)["temperature"], 0.4; got != want {
		t.Fatalf("params[temperature] = %v, want %v", got, want)
	}
	if got, want := (*model.Params)["top_p"], 0.95; got != want {
		t.Fatalf("params[top_p] = %v, want %v", got, want)
	}
	if model.ExtraParams == nil {
		t.Fatal("model.ExtraParams = nil, want parsed extra_params")
	}

	reasoningRaw, ok := (*model.ExtraParams)["reasoning"]
	if !ok {
		t.Fatal("extra_params[reasoning] missing")
	}
	reasoning, ok := reasoningRaw.(map[string]any)
	if !ok {
		t.Fatalf("extra_params[reasoning] type = %T, want map[string]any", reasoningRaw)
	}
	if got, want := reasoning["enabled"], false; got != want {
		t.Fatalf("extra_params[reasoning][enabled] = %v, want %v", got, want)
	}

	metadataRaw, ok := (*model.ExtraParams)["metadata"]
	if !ok {
		t.Fatal("extra_params[metadata] missing")
	}
	metadata, ok := metadataRaw.(map[string]any)
	if !ok {
		t.Fatalf("extra_params[metadata] type = %T, want map[string]any", metadataRaw)
	}
	if got, want := metadata["tier"], "offline"; got != want {
		t.Fatalf("extra_params[metadata][tier] = %v, want %v", got, want)
	}
}
