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

func codexTransportPtr(v CodexTransport) *CodexTransport {
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

func TestApplySubAgentPatchMaxParallel(t *testing.T) {
	tests := []struct {
		name    string
		initial SubAgentConfig
		patch   subAgentPatch
		want    SubAgentConfig
	}{
		{name: "unset max_parallel leaves default", initial: SubAgentConfig{MaxParallel: 3}, patch: subAgentPatch{}, want: SubAgentConfig{MaxParallel: 3}},
		{name: "zero max_parallel is preserved", initial: SubAgentConfig{MaxParallel: 3}, patch: subAgentPatch{MaxParallel: intPtr(0)}, want: SubAgentConfig{MaxParallel: 0}},
		{name: "sets max_parallel", initial: SubAgentConfig{MaxParallel: 3}, patch: subAgentPatch{MaxParallel: intPtr(4)}, want: SubAgentConfig{MaxParallel: 4}},
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

func TestApplyLimitsPatchMaxParallelTools(t *testing.T) {
	tests := []struct {
		name    string
		initial LimitsConfig
		patch   limitsPatch
		want    LimitsConfig
	}{
		{name: "unset max_parallel_tools leaves default", initial: LimitsConfig{MaxParallelTools: 4}, patch: limitsPatch{}, want: LimitsConfig{MaxParallelTools: 4}},
		{name: "zero max_parallel_tools is preserved", initial: LimitsConfig{MaxParallelTools: 4}, patch: limitsPatch{MaxParallelTools: intPtr(0)}, want: LimitsConfig{MaxParallelTools: 0}},
		{name: "sets max_parallel_tools", initial: LimitsConfig{MaxParallelTools: 4}, patch: limitsPatch{MaxParallelTools: intPtr(8)}, want: LimitsConfig{MaxParallelTools: 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyLimitsPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyLimitsPatch() = %#v, want %#v", dst, tt.want)
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
		check   func(*testing.T, Config)
	}{
		{
			name:    "sets default profile and definitions",
			initial: Config{},
			patch: modelsPatch{
				Profiles: &map[string]profilePatch{
					"default": {DefaultModel: stringPtr("default")},
				},
				Definitions: &map[string]modelPatch{
					"default": {Provider: stringPtr("local"), ID: stringPtr("model-id")},
				},
			},
			check: func(t *testing.T, cfg Config) {
				profile := cfg.Models.Profiles["default"]
				if profile.DefaultModel != "default" {
					t.Fatalf("default profile = %#v", profile)
				}
				if got := cfg.Models.Definitions["default"].ID; got != "model-id" {
					t.Fatalf("definition id = %q, want model-id", got)
				}
			},
		},
		{
			name:    "sets all profile assignments",
			initial: Config{},
			patch: modelsPatch{Profiles: &map[string]profilePatch{
				"default": {
					Advisor:         stringPtr("careful-model"),
					SubAgents:       stringMapPtr(map[string]string{"code": "fast-model"}),
					OneShot:         stringMapPtr(map[string]string{"plan": "planner-model"}),
					WorkflowHandoff: stringMapPtr(map[string]string{"review": "careful-model"}),
				},
			}},
			check: func(t *testing.T, cfg Config) {
				profile := cfg.Models.Profiles["default"]
				if profile.Advisor != "careful-model" || profile.SubAgents["code"] != "fast-model" || profile.OneShot["plan"] != "planner-model" || profile.WorkflowHandoff["review"] != "careful-model" {
					t.Fatalf("profile assignments = %#v", profile)
				}
			},
		},
		{
			name: "merges profile maps by key",
			initial: Config{Models: ModelsConfig{Profiles: map[string]ModelProfile{
				"default": {SubAgents: map[string]string{"code": "old", "explore": "old"}},
			}}},
			patch: modelsPatch{Profiles: &map[string]profilePatch{
				"default": {SubAgents: stringMapPtr(map[string]string{"code": "new"})},
			}},
			check: func(t *testing.T, cfg Config) {
				got := cfg.Models.Profiles["default"].SubAgents
				if got["code"] != "new" || got["explore"] != "old" {
					t.Fatalf("sub_agents = %#v", got)
				}
			},
		},
		{
			name: "omitted fields preserve profile",
			initial: Config{Models: ModelsConfig{Profiles: map[string]ModelProfile{
				"default": {DefaultModel: "existing", Advisor: "existing-advisor"},
			}}},
			patch: modelsPatch{},
			check: func(t *testing.T, cfg Config) {
				profile := cfg.Models.Profiles["default"]
				if profile.DefaultModel != "existing" || profile.Advisor != "existing-advisor" {
					t.Fatalf("profile = %#v", profile)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			applyModelsPatch(&cfg, &tt.patch)
			tt.check(t, cfg)
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
		{
			name:    "sets transport",
			initial: CodexConfig{Transport: CodexTransportHTTP},
			patch:   codexPatch{Transport: codexTransportPtr(CodexTransportWebSocket)},
			want:    CodexConfig{Transport: CodexTransportWebSocket},
		},
		{
			name:    "nil transport leaves value untouched",
			initial: CodexConfig{Transport: CodexTransportHTTP},
			patch:   codexPatch{},
			want:    CodexConfig{Transport: CodexTransportHTTP},
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
		{
			name: "applies codex transport patch",
			initial: ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					Transport: CodexTransportHTTP,
				},
			},
			patch: providerPatch{
				Codex: &codexPatch{
					Transport: codexTransportPtr(CodexTransportWebSocket),
				},
			},
			want: ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					Transport: CodexTransportWebSocket,
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

func TestApplyPermissionsPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial PermissionsConfig
		patch   permissionsPatch
		want    PermissionsConfig
	}{
		{
			name:    "sets docker",
			initial: PermissionsConfig{Docker: false},
			patch:   permissionsPatch{Docker: boolPtr(true)},
			want:    PermissionsConfig{Docker: true},
		},
		{
			name:    "nil docker leaves value untouched",
			initial: PermissionsConfig{Docker: true},
			patch:   permissionsPatch{},
			want:    PermissionsConfig{Docker: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyPermissionsPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyPermissionsPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplySandboxPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial SandboxConfig
		patch   sandboxPatch
		want    SandboxConfig
	}{
		{
			name:    "sets enabled and warning flags",
			initial: SandboxConfig{},
			patch:   sandboxPatch{Enabled: boolPtr(true), WarningOnUnsupportedPlatform: boolPtr(true)},
			want:    SandboxConfig{Enabled: true, WarningOnUnsupportedPlatform: true},
		},
		{
			name:    "sets env passthrough fields",
			initial: SandboxConfig{},
			patch:   sandboxPatch{EnvPassthrough: stringSlicePtr([]string{"MYAPP_*"}), EnvPassthroughAll: boolPtr(true)},
			want:    SandboxConfig{EnvPassthrough: []string{"MYAPP_*"}, EnvPassthroughAll: true},
		},
		{
			name:    "nil fields leave values untouched",
			initial: SandboxConfig{Enabled: true, EnvPassthrough: []string{"FOO"}, EnvPassthroughAll: true},
			patch:   sandboxPatch{},
			want:    SandboxConfig{Enabled: true, EnvPassthrough: []string{"FOO"}, EnvPassthroughAll: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applySandboxPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applySandboxPatch() = %#v, want %#v", dst, tt.want)
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
		{
			name: "patches approval and trust_annotations",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Approval: "ask", TrustAnnotations: false},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Approval: stringPtr("allow"), TrustAnnotations: boolPtr(true)},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Approval: "allow", TrustAnnotations: true},
				},
			},
		},
		{
			name: "sets url field",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http"},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {URL: stringPtr("http://localhost:3000")},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000"},
				},
			},
		},
		{
			name: "merges headers per-key",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Headers: map[string]string{"Authorization": "Bearer token1"}},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Headers: stringMapPtr(map[string]string{"X-Custom": "value"})},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Headers: map[string]string{"Authorization": "Bearer token1", "X-Custom": "value"}},
				},
			},
		},
		{
			name: "headers merge overwrites existing keys",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Headers: map[string]string{"Authorization": "Bearer old"}},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Headers: stringMapPtr(map[string]string{"Authorization": "Bearer new"})},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Headers: map[string]string{"Authorization": "Bearer new"}},
				},
			},
		},
		{
			name: "allocates headers map when nil",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Headers: stringMapPtr(map[string]string{"X-Custom": "value"})},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Headers: map[string]string{"X-Custom": "value"}},
				},
			},
		},
		{
			name: "patches allowed_tools, blocked_tools, and sub_agents",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {AllowedTools: []string{"old"}, BlockedTools: []string{"old"}, SubAgents: []string{"review"}},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {AllowedTools: stringSlicePtr([]string{"echo"}), BlockedTools: stringSlicePtr([]string{"dangerous"}), SubAgents: stringSlicePtr([]string{"explore", "code"})},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {AllowedTools: []string{"echo"}, BlockedTools: []string{"dangerous"}, SubAgents: []string{"explore", "code"}},
				},
			},
		},
		{
			name: "nil filter fields leave values untouched",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {AllowedTools: []string{"echo"}, BlockedTools: []string{"dangerous"}, SubAgents: []string{"explore"}},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {Approval: stringPtr("allow")},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {AllowedTools: []string{"echo"}, BlockedTools: []string{"dangerous"}, SubAgents: []string{"explore"}, Approval: "allow"},
				},
			},
		},
		{
			name: "explicit empty allowed_tools replaces with empty",
			initial: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {AllowedTools: []string{"echo"}},
				},
			},
			patch: mcpPatch{
				Servers: &map[string]mcpServerPatch{
					"example": {AllowedTools: stringSlicePtr([]string{})},
				},
			},
			want: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {AllowedTools: []string{}},
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

func TestModelsPatchRoundTripsDiscoveryEnabled(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{name: "omitted keeps default", yaml: "models: {}\n", want: true},
		{name: "explicit false", yaml: "models:\n  discovery_enabled: false\n", want: false},
		{name: "explicit true", yaml: "models:\n  discovery_enabled: true\n", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := parseConfigPatch(tt.yaml)
			if err != nil {
				t.Fatalf("parseConfigPatch() error = %v", err)
			}
			cfg := defaultConfig()
			applyPatch(&cfg, patch)
			if cfg.Models.DiscoveryEnabled != tt.want {
				t.Fatalf("Models.DiscoveryEnabled = %v, want %v", cfg.Models.DiscoveryEnabled, tt.want)
			}
		})
	}
}

func TestModelDefinitionPatchPreservesSupportedEffortsNilAndEmpty(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "omitted stays nil",
			yaml: "models:\n  definitions:\n    custom:\n      advanced:\n        reasoning: {}\n",
			want: nil,
		},
		{
			name: "explicit empty stays empty",
			yaml: "models:\n  definitions:\n    custom:\n      advanced:\n        reasoning:\n          supported_efforts: []\n",
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := parseConfigPatch(tt.yaml)
			if err != nil {
				t.Fatalf("parseConfigPatch() error = %v", err)
			}
			cfg := defaultConfig()
			applyPatch(&cfg, patch)
			got := cfg.Models.Definitions["custom"].Advanced.Reasoning.SupportedEfforts
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SupportedEfforts = %#v, want %#v", got, tt.want)
			}
		})
	}
}
