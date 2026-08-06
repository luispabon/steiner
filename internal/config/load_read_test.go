package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseConfigPatchPreservesParamsAndExtraParams(t *testing.T) {
	patch, err := parseConfigPatch(`models:
  definitions:
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
      prompt_suffix: <|think_off|>
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.Models == nil || patch.Models.Definitions == nil {
		t.Fatal("patch.Models.Definitions = nil, want parsed model patch")
	}

	model, ok := (*patch.Models.Definitions)["default"]
	if !ok {
		t.Fatal("patch.Models.Definitions[default] missing")
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
	if model.PromptSuffix == nil {
		t.Fatal("model.PromptSuffix = nil, want parsed prompt_suffix")
	}
	if got, want := *model.PromptSuffix, "<|think_off|>"; got != want {
		t.Fatalf("prompt_suffix = %q, want %q", got, want)
	}
}

func TestParseConfigPatchModelsSubAgents(t *testing.T) {
	patch, err := parseConfigPatch(`sub_agent:
  enabled: true
  max_turns: 10
  max_tokens: 50000
models:
  sub_agents:
    code: fast-model
    explore: thorough-model
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.SubAgent == nil {
		t.Fatal("patch.SubAgent = nil, want parsed sub_agent patch")
	}
	if patch.Models == nil || patch.Models.SubAgents == nil {
		t.Fatal("patch.Models.SubAgents = nil, want parsed sub_agents map")
	}
	agents := *patch.Models.SubAgents
	if got, want := len(agents), 2; got != want {
		t.Fatalf("len(agents) = %d, want %d", got, want)
	}
	if got, want := agents["code"], "fast-model"; got != want {
		t.Fatalf("sub_agents[code] = %q, want %q", got, want)
	}
	if got, want := agents["explore"], "thorough-model"; got != want {
		t.Fatalf("sub_agents[explore] = %q, want %q", got, want)
	}
}

func TestSubAgentConfigYAMLParsing(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want SubAgentConfig
	}{
		{
			name: "parses scalar fields",
			yaml: `sub_agent:
  enabled: true
  max_turns: 5
  max_tokens: 10000
`,
			want: SubAgentConfig{
				Enabled:   true,
				MaxTurns:  5,
				MaxTokens: 10000,
			},
		},
		{
			name: "disabled",
			yaml: `sub_agent:
  enabled: false
  max_turns: 3
  max_tokens: 5000
`,
			want: SubAgentConfig{
				Enabled:   false,
				MaxTurns:  3,
				MaxTokens: 5000,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := parseConfigPatch(tt.yaml)
			if err != nil {
				t.Fatalf("parseConfigPatch() error = %v", err)
			}
			if patch.SubAgent == nil {
				t.Fatal("patch.SubAgent = nil, want parsed sub_agent patch")
			}
			// Apply patch to an empty SubAgentConfig to verify round-trip.
			dst := SubAgentConfig{}
			applySubAgentPatch(&dst, patch.SubAgent)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applySubAgentPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestParseConfigPatchRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "top_level_unknown",
			yaml: `unknown_section:
  enabled: true
`,
			wantErr: "field unknown_section not found",
		},
		{
			name: "nested_unknown",
			yaml: `models:
  definitions:
    default:
      provider: local
      id: qwen3
      unexpected_field: value
`,
			wantErr: "field unexpected_field not found",
		},
		{
			name: "unknown_nested_section",
			yaml: `sub_agent:
  enabled: true
  max_turns: 5
  max_tokens: 10000
  extra_settings:
    enabled: false
`,
			wantErr: "field extra_settings not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfigPatch(tt.yaml)
			if err == nil {
				t.Fatalf("parseConfigPatch() error = nil, want substring %q", tt.wantErr)
			}
			if got := err.Error(); !strings.Contains(got, tt.wantErr) {
				t.Fatalf("parseConfigPatch() error = %q, want substring %q", got, tt.wantErr)
			}
		})
	}
}

// parseConfigPatch reads a YAML config snippet and returns it as a patch.
// This is a test-only helper used by config unit tests.
func parseConfigPatch(contents string) (configPatch, error) {
	const path = "test.yaml"
	root, err := decodeConfigNode(path, contents)
	if err != nil {
		return configPatch{}, err
	}

	if root.Kind == 0 {
		return configPatch{}, nil
	}

	cleaned, err := marshalCleanConfigNode(path, root)
	if err != nil {
		return configPatch{}, err
	}
	var patch configPatch
	if err := decodeKnownConfigPatch(cleaned, &patch); err != nil {
		return configPatch{}, err
	}
	return patch, nil
}
