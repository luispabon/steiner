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

func TestApplyWorkflowHandoffPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial workflowHandoffConfig
		patch   workflowHandoffPatch
		want    workflowHandoffConfig
	}{
		{
			name:    "sets workflow aliases",
			initial: workflowHandoffConfig{},
			patch: workflowHandoffPatch{
				Models: stringMapPtr(map[string]string{
					"implement": "fast-model",
					"review":    "careful-model",
				}),
			},
			want: workflowHandoffConfig{
				Models: map[string]string{
					"implement": "fast-model",
					"review":    "careful-model",
				},
			},
		},
		{
			name: "partial override preserves existing entries",
			initial: workflowHandoffConfig{
				Models: map[string]string{
					"implement": "existing-implement",
					"review":    "existing-review",
				},
			},
			patch: workflowHandoffPatch{
				Models: stringMapPtr(map[string]string{
					"review": "new-review",
				}),
			},
			want: workflowHandoffConfig{
				Models: map[string]string{
					"implement": "existing-implement",
					"review":    "new-review",
				},
			},
		},
		{
			name: "nil models leaves value untouched",
			initial: workflowHandoffConfig{
				Models: map[string]string{
					"implement": "existing",
				},
			},
			patch: workflowHandoffPatch{},
			want: workflowHandoffConfig{
				Models: map[string]string{
					"implement": "existing",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyWorkflowHandoffPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyWorkflowHandoffPatch() = %#v, want %#v", dst, tt.want)
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
}

func pointerProviderType(pt ProviderType) *ProviderType {
	return &pt
}

func agentConfigPatchMapPtr(v map[string]agentConfigPatch) *map[string]agentConfigPatch {
	return &v
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
		{
			name:    "sets allowed_tools",
			initial: SubAgentConfig{AllowedTools: []string{"read"}},
			patch:   subAgentPatch{AllowedTools: &[]string{"read", "bash"}},
			want:    SubAgentConfig{AllowedTools: []string{"read", "bash"}},
		},
		{
			name:    "adds new agent entry",
			initial: SubAgentConfig{},
			patch: subAgentPatch{
				Agents: agentConfigPatchMapPtr(map[string]agentConfigPatch{
					"code": {Model: stringPtr("fast-model")},
				}),
			},
			want: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"code": {Model: "fast-model"},
				},
			},
		},
		{
			name: "updates existing agent model",
			initial: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"explore": {Model: "old-model"},
				},
			},
			patch: subAgentPatch{
				Agents: agentConfigPatchMapPtr(map[string]agentConfigPatch{
					"explore": {Model: stringPtr("new-model")},
				}),
			},
			want: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"explore": {Model: "new-model"},
				},
			},
		},
		{
			name: "nil model in agent patch leaves model untouched",
			initial: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"plan": {Model: "existing-model"},
				},
			},
			patch: subAgentPatch{
				Agents: agentConfigPatchMapPtr(map[string]agentConfigPatch{
					"plan": {},
				}),
			},
			want: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"plan": {Model: "existing-model"},
				},
			},
		},
		{
			name: "nil agents patch leaves agents untouched",
			initial: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"verify": {Model: "verify-model"},
				},
			},
			patch: subAgentPatch{},
			want: SubAgentConfig{
				Agents: map[string]AgentConfig{
					"verify": {Model: "verify-model"},
				},
			},
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
