package config

import (
	"reflect"
	"testing"
)

func intPtr(v int) *int                { return &v }
func stringPtr(v string) *string       { return &v }
func boolPtr(v bool) *bool             { return &v }
func durationPtr(v Duration) *Duration { return &v }
func modelTransportTypePtr(v ModelTransportType) *ModelTransportType {
	return &v
}

func stringAnyMapPtr(v map[string]any) *map[string]any {
	return &v
}

func stringMapPtr(v map[string]string) *map[string]string {
	return &v
}

func stringSlicePtr(v []string) *[]string {
	return &v
}

func TestApplySchedulerPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial SchedulerConfig
		patch   schedulerPatch
		want    SchedulerConfig
	}{
		{
			name:    "sets parallelism",
			initial: SchedulerConfig{Parallelism: 1},
			patch:   schedulerPatch{Parallelism: intPtr(4)},
			want:    SchedulerConfig{Parallelism: 4},
		},
		{
			name:    "nil parallelism leaves value untouched",
			initial: SchedulerConfig{Parallelism: 2},
			patch:   schedulerPatch{},
			want:    SchedulerConfig{Parallelism: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applySchedulerPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applySchedulerPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyProviderPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial ProviderConfig
		patch   providerPatch
		want    ProviderConfig
	}{
		{
			name: "sets type and base_url",
			initial: ProviderConfig{
				Type:    ProviderTypeOpenAICompat,
				BaseURL: "http://old:11434/v1",
			},
			patch: providerPatch{
				Type:    pointerProviderType(ProviderTypeOllama),
				BaseURL: stringPtr("http://new:11434/v1"),
			},
			want: ProviderConfig{
				Type:    ProviderTypeOllama,
				BaseURL: "http://new:11434/v1",
			},
		},
		{
			name: "nil fields leave values untouched",
			initial: ProviderConfig{
				Type:    ProviderTypeOpenAICompat,
				BaseURL: "http://localhost:11434/v1",
				APIKey:  "old-key",
			},
			patch: providerPatch{
				APIKey: stringPtr("new-key"),
			},
			want: ProviderConfig{
				Type:    ProviderTypeOpenAICompat,
				BaseURL: "http://localhost:11434/v1",
				APIKey:  "new-key",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyProviderPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyProviderPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyModelPatch(t *testing.T) {
	retry250ms := MustDuration("250ms")
	retry1s := MustDuration("1s")
	retry5s := MustDuration("5s")
	retry10s := MustDuration("10s")
	retry30s := MustDuration("30s")
	tests := []struct {
		name    string
		initial ModelConfig
		patch   modelPatch
		want    ModelConfig
	}{
		{
			name: "sets all scalar fields",
			initial: ModelConfig{
				Provider: "local",
				ID:       "old-model",
				Retry: RetryConfig{
					Enabled:        true,
					MaxAttempts:    3,
					InitialBackoff: retry250ms,
					MaxBackoff:     retry5s,
					RetryAfterMax:  retry30s,
				},
			},
			patch: modelPatch{
				Provider: stringPtr("remote"),
				ID:       stringPtr("new-model"),
				Retry: &retryPatch{
					Enabled:        boolPtr(false),
					MaxAttempts:    intPtr(7),
					InitialBackoff: durationPtr(retry1s),
					MaxBackoff:     durationPtr(retry10s),
					RetryAfterMax:  durationPtr(retry30s),
				},
			},
			want: ModelConfig{
				Provider: "remote",
				ID:       "new-model",
				Retry: RetryConfig{
					Enabled:        false,
					MaxAttempts:    7,
					InitialBackoff: retry1s,
					MaxBackoff:     retry10s,
					RetryAfterMax:  retry30s,
				},
			},
		},
		{
			name: "nil fields leave existing values untouched",
			initial: ModelConfig{
				Provider: "local",
				ID:       "existing-model",
			},
			patch: modelPatch{
				ID: stringPtr("updated-model"),
			},
			want: ModelConfig{
				Provider: "local",
				ID:       "updated-model",
			},
		},
		{
			name: "sets prompt suffix",
			initial: ModelConfig{
				Provider:     "local",
				ID:           "model",
				PromptSuffix: "old",
			},
			patch: modelPatch{
				PromptSuffix: stringPtr("<|think_off|>"),
			},
			want: ModelConfig{
				Provider:     "local",
				ID:           "model",
				PromptSuffix: "<|think_off|>",
			},
		},
		{
			name: "sets Params map via copy",
			initial: ModelConfig{
				Params: map[string]any{"existing": "value"},
			},
			patch: modelPatch{
				Params: stringAnyMapPtr(map[string]any{"temperature": 0.7}),
			},
			want: ModelConfig{
				Params: map[string]any{"temperature": 0.7},
			},
		},
		{
			name: "sets ExtraParams map via copy",
			initial: ModelConfig{
				ExtraParams: map[string]any{"existing": "value"},
			},
			patch: modelPatch{
				ExtraParams: stringAnyMapPtr(map[string]any{"temperature": 0.7, "top_p": 0.9}),
			},
			want: ModelConfig{
				ExtraParams: map[string]any{"temperature": 0.7, "top_p": 0.9},
			},
		},
		{
			name: "applies advanced sub-patch",
			initial: ModelConfig{
				Advanced: AdvancedConfig{
					Limits: AdvancedLimitsConfig{
						ContextWindow:   16384,
						MaxOutputTokens: 4096,
					},
					Transport: ModelTransportAuto,
				},
			},
			patch: modelPatch{
				Advanced: &advancedPatch{
					Limits: &advancedLimitsPatch{
						ContextWindow:   intPtr(32768),
						MaxOutputTokens: intPtr(8192),
					},
					Transport: modelTransportTypePtr(ModelTransportAnthropic),
				},
			},
			want: ModelConfig{
				Advanced: AdvancedConfig{
					Limits: AdvancedLimitsConfig{
						ContextWindow:   32768,
						MaxOutputTokens: 8192,
					},
					Transport: ModelTransportAnthropic,
				},
			},
		},
		{
			name: "applies reasoning_echo_back sub-patch",
			initial: ModelConfig{
				Advanced: AdvancedConfig{
					ReasoningEchoBack: boolPtr(false),
				},
			},
			patch: modelPatch{
				Advanced: &advancedPatch{
					ReasoningEchoBack: boolPtr(true),
				},
			},
			want: ModelConfig{
				Advanced: AdvancedConfig{
					ReasoningEchoBack: boolPtr(true),
				},
			},
		},
		{
			name: "applies reasoning sub-patch",
			initial: ModelConfig{
				Advanced: AdvancedConfig{
					Reasoning: ReasoningConfig{
						Effort:           "low",
						SupportedEfforts: []string{"low", "medium"},
					},
				},
			},
			patch: modelPatch{
				Advanced: &advancedPatch{
					Reasoning: &reasoningPatch{
						Effort:           stringPtr("high"),
						SupportedEfforts: stringSlicePtr([]string{"low", "medium", "high"}),
					},
				},
			},
			want: ModelConfig{
				Advanced: AdvancedConfig{
					Reasoning: ReasoningConfig{
						Effort:           "high",
						SupportedEfforts: []string{"low", "medium", "high"},
					},
				},
			},
		},
		{
			name: "reasoning sub-patch with nil fields leaves existing values untouched",
			initial: ModelConfig{
				Advanced: AdvancedConfig{
					Reasoning: ReasoningConfig{
						Effort:           "low",
						SupportedEfforts: []string{"low", "medium"},
					},
				},
			},
			patch: modelPatch{
				Advanced: &advancedPatch{
					Reasoning: &reasoningPatch{},
				},
			},
			want: ModelConfig{
				Advanced: AdvancedConfig{
					Reasoning: ReasoningConfig{
						Effort:           "low",
						SupportedEfforts: []string{"low", "medium"},
					},
				},
			},
		},
		{
			name: "applies retry sub-patch",
			initial: ModelConfig{
				Retry: RetryConfig{
					Enabled:        true,
					MaxAttempts:    3,
					InitialBackoff: retry250ms,
					MaxBackoff:     retry5s,
					RetryAfterMax:  retry30s,
				},
			},
			patch: modelPatch{
				Retry: &retryPatch{
					Enabled:        boolPtr(false),
					MaxAttempts:    intPtr(5),
					InitialBackoff: durationPtr(retry1s),
					MaxBackoff:     durationPtr(retry10s),
					RetryAfterMax:  durationPtr(retry30s),
				},
			},
			want: ModelConfig{
				Retry: RetryConfig{
					Enabled:        false,
					MaxAttempts:    5,
					InitialBackoff: retry1s,
					MaxBackoff:     retry10s,
					RetryAfterMax:  retry30s,
				},
			},
		},
		{
			name: "applies prompts sub-patch",
			initial: ModelConfig{
				Prompts: ModelPrompts{System: "old", Compaction: "old"},
			},
			patch: modelPatch{
				Prompts: &modelPromptsPatch{
					System: stringPtr("new"),
				},
			},
			want: ModelConfig{
				Prompts: ModelPrompts{System: "new", Compaction: "old"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyModelPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyModelPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyModelsPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial Config
		patch   modelsPatch
		want    ModelsConfig
	}{
		{
			name:    "sets default and definitions",
			initial: Config{},
			patch: modelsPatch{
				Default: stringPtr("default"),
				Definitions: &map[string]modelPatch{
					"default": {Provider: stringPtr("local"), ID: stringPtr("model-id")},
				},
			},
			want: ModelsConfig{
				Default: "default",
				Definitions: map[string]ModelConfig{
					"default": {Provider: "local", ID: "model-id"},
				},
			},
		},
		{
			name:    "sets advisor, sub_agents, oneshot, workflow_handoff aliases",
			initial: Config{},
			patch: modelsPatch{
				Advisor:   stringPtr("careful-model"),
				SubAgents: stringMapPtr(map[string]string{"code": "fast-model"}),
				OneShot: stringMapPtr(map[string]string{
					"plan":      "planner-model",
					"implement": "implement-model",
					"review":    "review-model",
				}),
				WorkflowHandoff: stringMapPtr(map[string]string{
					"implement": "fast-model",
					"review":    "careful-model",
				}),
			},
			want: ModelsConfig{
				Advisor:   "careful-model",
				SubAgents: map[string]string{"code": "fast-model"},
				OneShot: map[string]string{
					"plan":      "planner-model",
					"implement": "implement-model",
					"review":    "review-model",
				},
				WorkflowHandoff: map[string]string{
					"implement": "fast-model",
					"review":    "careful-model",
				},
			},
		},
		{
			name: "partial override preserves existing map entries",
			initial: Config{
				Models: ModelsConfig{
					SubAgents: map[string]string{"code": "existing-code", "plan": "existing-plan"},
					OneShot:   map[string]string{"plan": "existing-plan"},
					WorkflowHandoff: map[string]string{
						"implement": "existing-implement",
						"review":    "existing-review",
					},
				},
			},
			patch: modelsPatch{
				SubAgents:       stringMapPtr(map[string]string{"code": "new-code"}),
				OneShot:         stringMapPtr(map[string]string{"review": "new-review"}),
				WorkflowHandoff: stringMapPtr(map[string]string{"review": "new-review"}),
			},
			want: ModelsConfig{
				SubAgents: map[string]string{"code": "new-code", "plan": "existing-plan"},
				OneShot:   map[string]string{"plan": "existing-plan", "review": "new-review"},
				WorkflowHandoff: map[string]string{
					"implement": "existing-implement",
					"review":    "new-review",
				},
			},
		},
		{
			name: "nil fields leave values untouched",
			initial: Config{
				Models: ModelsConfig{
					Default: "existing",
					Advisor: "existing-advisor",
				},
			},
			patch: modelsPatch{},
			want: ModelsConfig{
				Default: "existing",
				Advisor: "existing-advisor",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			applyModelsPatch(&cfg, &tt.patch)
			if !reflect.DeepEqual(cfg.Models, tt.want) {
				t.Fatalf("applyModelsPatch() = %#v, want %#v", cfg.Models, tt.want)
			}
		})
	}
}

func TestCloneModelConfig(t *testing.T) {
	original := ModelConfig{
		Provider:     "local",
		ID:           "test-model",
		Params:       map[string]any{"key": "value"},
		ExtraParams:  map[string]any{"extra": "param"},
		PromptSuffix: "<|think_off|>",
		Advanced: AdvancedConfig{
			Reasoning: ReasoningConfig{
				Effort:           "high",
				SupportedEfforts: []string{"low", "high"},
			},
		},
	}

	cloned := cloneModelConfig(original)

	if !reflect.DeepEqual(cloned, original) {
		t.Fatalf("cloneModelConfig() = %#v, want %#v", cloned, original)
	}

	// Verify maps are separate
	cloned.Params["key"] = "modified"
	if original.Params["key"] == "modified" {
		t.Fatal("cloneModelConfig() did not copy Params map separately")
	}

	cloned.ExtraParams["extra"] = "modified"
	if original.ExtraParams["extra"] == "modified" {
		t.Fatal("cloneModelConfig() did not copy ExtraParams map separately")
	}

	cloned.Advanced.Reasoning.SupportedEfforts[0] = "modified"
	if original.Advanced.Reasoning.SupportedEfforts[0] == "modified" {
		t.Fatal("cloneModelConfig() did not copy Advanced.Reasoning.SupportedEfforts separately")
	}
}

func pointerProviderType(pt ProviderType) *ProviderType {
	return &pt
}

func TestApplySubAgentPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial SubAgentConfig
		patch   subAgentPatch
		want    SubAgentConfig
	}{
		{
			name:    "sets enabled",
			initial: SubAgentConfig{Enabled: false},
			patch:   subAgentPatch{Enabled: boolPtr(true)},
			want:    SubAgentConfig{Enabled: true},
		},
		{
			name:    "sets max_turns and max_tokens",
			initial: SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 1000},
			patch:   subAgentPatch{MaxTurns: intPtr(10), MaxTokens: intPtr(2000)},
			want:    SubAgentConfig{Enabled: true, MaxTurns: 10, MaxTokens: 2000},
		},
		{
			name:    "nil fields leave values untouched",
			initial: SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 1000},
			patch:   subAgentPatch{},
			want:    SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 1000},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applySubAgentPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applySubAgentPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyCodexPatch(t *testing.T) {
	interval4s := MustDuration("4s")
	interval2s := MustDuration("2s")
	tests := []struct {
		name    string
		initial CodexConfig
		patch   codexPatch
		want    CodexConfig
	}{
		{
			name:    "sets min_request_interval",
			initial: CodexConfig{MinRequestInterval: interval4s},
			patch:   codexPatch{MinRequestInterval: durationPtr(interval2s)},
			want:    CodexConfig{MinRequestInterval: interval2s},
		},
		{
			name:    "nil interval leaves value untouched",
			initial: CodexConfig{MinRequestInterval: interval4s},
			patch:   codexPatch{},
			want:    CodexConfig{MinRequestInterval: interval4s},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyCodexPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyCodexPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyProviderPatchWithCodex(t *testing.T) {
	interval2s := MustDuration("2s")
	interval4s := MustDuration("4s")
	tests := []struct {
		name    string
		initial ProviderConfig
		patch   providerPatch
		want    ProviderConfig
	}{
		{
			name: "applies codex patch",
			initial: ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					MinRequestInterval: interval4s,
				},
			},
			patch: providerPatch{
				Codex: &codexPatch{
					MinRequestInterval: durationPtr(interval2s),
				},
			},
			want: ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					MinRequestInterval: interval2s,
				},
			},
		},
		{
			name: "nil codex patch leaves codex config untouched",
			initial: ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					MinRequestInterval: interval4s,
				},
			},
			patch: providerPatch{},
			want: ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					MinRequestInterval: interval4s,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyProviderPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyProviderPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func executionModePtr(v ExecutionMode) *ExecutionMode {
	return &v
}

func TestApplyModesPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial ModesConfig
		patch   modesPatch
		want    ModesConfig
	}{
		{
			name:    "sets default execution mode",
			initial: ModesConfig{Default: ExecutionModeBuild},
			patch:   modesPatch{Default: executionModePtr(ExecutionModePlan)},
			want:    ModesConfig{Default: ExecutionModePlan},
		},
		{
			name:    "nil patch leaves mode untouched",
			initial: ModesConfig{Default: ExecutionModeBuild},
			patch:   modesPatch{},
			want:    ModesConfig{Default: ExecutionModeBuild},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyModesPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyModesPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyMCPPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial MCPConfig
		patch   mcpPatch
		want    MCPConfig
	}{
		{
			name:    "applies top-level enabled",
			initial: MCPConfig{},
			patch:   mcpPatch{Enabled: boolPtr(true)},
			want:    MCPConfig{Enabled: true},
		},
		{
			name:    "adds a new server",
			initial: MCPConfig{},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Enabled: boolPtr(true), Transport: stringPtr("stdio"), Command: stringPtr("npx")},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Enabled: true, Transport: "stdio", Command: "npx"},
				},
			},
		},
		{
			name: "partial server override preserves fields",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Enabled: true, Transport: "stdio", Command: "npx"},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Enabled: boolPtr(false)},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Enabled: false, Transport: "stdio", Command: "npx"},
				},
			},
		},
		{
			name: "merges env per-key",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Env: map[string]string{"A": "1"}},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Env: stringMapPtr(map[string]string{"B": "2"})},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Env: map[string]string{"A": "1", "B": "2"}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyMCPPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyMCPPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}
