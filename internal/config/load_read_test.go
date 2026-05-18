package config

import (
	"reflect"
	"testing"
)

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
    prompt_suffix: <|think_off|>
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
	if model.PromptSuffix == nil {
		t.Fatal("model.PromptSuffix = nil, want parsed prompt_suffix")
	}
	if got, want := *model.PromptSuffix, "<|think_off|>"; got != want {
		t.Fatalf("prompt_suffix = %q, want %q", got, want)
	}
}

func TestParseConfigPatchSubAgentAgents(t *testing.T) {
	patch, err := parseConfigPatch("test.yaml", `sub_agent:
  enabled: true
  max_turns: 10
  max_tokens: 50000
  agents:
    code:
      model: fast-model
    explore:
      model: thorough-model
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.SubAgent == nil {
		t.Fatal("patch.SubAgent = nil, want parsed sub_agent patch")
	}
	if patch.SubAgent.Agents == nil {
		t.Fatal("patch.SubAgent.Agents = nil, want parsed agents map")
	}
	agents := *patch.SubAgent.Agents
	if got, want := len(agents), 2; got != want {
		t.Fatalf("len(agents) = %d, want %d", got, want)
	}
	codeAgent, ok := agents["code"]
	if !ok {
		t.Fatal("agents[code] missing")
	}
	if codeAgent.Model == nil {
		t.Fatal("agents[code].model = nil, want parsed model")
	}
	if got, want := *codeAgent.Model, "fast-model"; got != want {
		t.Fatalf("agents[code].model = %q, want %q", got, want)
	}
	exploreAgent, ok := agents["explore"]
	if !ok {
		t.Fatal("agents[explore] missing")
	}
	if exploreAgent.Model == nil {
		t.Fatal("agents[explore].model = nil, want parsed model")
	}
	if got, want := *exploreAgent.Model, "thorough-model"; got != want {
		t.Fatalf("agents[explore].model = %q, want %q", got, want)
	}
}

func TestSubAgentConfigYAMLParsing(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want SubAgentConfig
	}{
		{
			name: "parses agents map",
			yaml: `sub_agent:
  enabled: true
  max_turns: 5
  max_tokens: 10000
  agents:
    code:
      model: code-model
    verify:
      model: verify-model
`,
			want: SubAgentConfig{
				Enabled:   true,
				MaxTurns:  5,
				MaxTokens: 10000,
				Agents: map[string]AgentConfig{
					"code":   {Model: "code-model"},
					"verify": {Model: "verify-model"},
				},
			},
		},
		{
			name: "empty agents map",
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
			patch, err := parseConfigPatch("test.yaml", tt.yaml)
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
