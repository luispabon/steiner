package config

import (
	"reflect"
	"testing"
)

func intPtr(v int) *int                            { return &v }
func stringPtr(v string) *string                   { return &v }
func boolPtr(v bool) *bool                         { return &v }
func approvalModePtr(v ApprovalMode) *ApprovalMode { return &v }
func durationPtr(v Duration) *Duration             { return &v }
func debugPatchPtr(v debugPatch) *debugPatch       { return &v }
func retryPatchPtr(v retryPatch) *retryPatch       { return &v }

func stringSlicePtr(v ...string) *[]string {
	s := append([]string(nil), v...)
	return &s
}

func durationMapPtr(v map[string]Duration) *map[string]Duration {
	return &v
}

func approvalModeMapPtr(v map[string]*ApprovalMode) *map[string]*ApprovalMode {
	return &v
}

func stringAnyMapPtr(v map[string]any) *map[string]any {
	return &v
}

func modelPatchMapPtr(v map[string]modelPatch) *map[string]modelPatch {
	return &v
}

func modelPromptsPatchPtr(v modelPromptsPatch) *modelPromptsPatch {
	return &v
}

func toolPatchMapPtr(v map[string]toolPatch) *map[string]toolPatch {
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
				Type:                "ollama",
				BaseURL:             "http://old:11434/v1",
				APIKey:              "old-key",
				Model:               "old-model",
				MaxCompletionTokens: 4096,
				ContextSize:         16384,
				Retry: RetryConfig{
					Enabled:        true,
					MaxAttempts:    3,
					InitialBackoff: retry250ms,
					MaxBackoff:     retry5s,
					RetryAfterMax:  retry30s,
				},
			},
			patch: modelPatch{
				Type:                stringPtr("openai_compat"),
				BaseURL:             stringPtr("http://new:11434/v1"),
				APIKey:              stringPtr("new-key"),
				Model:               stringPtr("new-model"),
				MaxCompletionTokens: intPtr(8192),
				ContextSize:         intPtr(32768),
				Retry: &retryPatch{
					Enabled:        boolPtr(false),
					MaxAttempts:    intPtr(7),
					InitialBackoff: durationPtr(retry1s),
					MaxBackoff:     durationPtr(retry10s),
					RetryAfterMax:  durationPtr(retry30s),
				},
			},
			want: ModelConfig{
				Type:                "openai_compat",
				BaseURL:             "http://new:11434/v1",
				APIKey:              "new-key",
				Model:               "new-model",
				MaxCompletionTokens: 8192,
				ContextSize:         32768,
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
				Type:    "ollama",
				Model:   "existing-model",
				BaseURL: "http://existing:11434/v1",
			},
			patch: modelPatch{
				MaxCompletionTokens: intPtr(2048),
			},
			want: ModelConfig{
				Type:                "ollama",
				Model:               "existing-model",
				BaseURL:             "http://existing:11434/v1",
				MaxCompletionTokens: 2048,
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
			name:    "ExtraParams replacement does not share backing map",
			initial: ModelConfig{},
			patch: modelPatch{
				ExtraParams: stringAnyMapPtr(map[string]any{"key": "val"}),
			},
			want: ModelConfig{
				ExtraParams: map[string]any{"key": "val"},
			},
		},
		{
			name: "applies compaction sub-patch",
			initial: ModelConfig{
				Compaction: CompactionConfig{SafetyMarginTokens: 256, SummaryMaxTokens: 128},
			},
			patch: modelPatch{
				Compaction: &compactionPatch{
					SafetyMarginTokens: intPtr(1024),
					SummaryMaxTokens:   intPtr(512),
				},
			},
			want: ModelConfig{
				Compaction: CompactionConfig{SafetyMarginTokens: 1024, SummaryMaxTokens: 512},
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
					MaxBackoff:     durationPtr(retry30s),
					RetryAfterMax:  durationPtr(retry5s),
				},
			},
			want: ModelConfig{
				Retry: RetryConfig{
					Enabled:        false,
					MaxAttempts:    5,
					InitialBackoff: retry1s,
					MaxBackoff:     retry30s,
					RetryAfterMax:  retry5s,
				},
			},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: ModelConfig{Type: "ollama", Model: "qwen", BaseURL: "http://localhost:11434/v1"},
			patch:   modelPatch{},
			want:    ModelConfig{Type: "ollama", Model: "qwen", BaseURL: "http://localhost:11434/v1"},
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

func TestApplyCompactionPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial CompactionConfig
		patch   compactionPatch
		want    CompactionConfig
	}{
		{
			name:    "sets both fields",
			initial: CompactionConfig{SafetyMarginTokens: 512, SummaryMaxTokens: 256},
			patch:   compactionPatch{SafetyMarginTokens: intPtr(1024), SummaryMaxTokens: intPtr(512)},
			want:    CompactionConfig{SafetyMarginTokens: 1024, SummaryMaxTokens: 512},
		},
		{
			name:    "sets only safety margin",
			initial: CompactionConfig{SafetyMarginTokens: 512, SummaryMaxTokens: 256},
			patch:   compactionPatch{SafetyMarginTokens: intPtr(2048)},
			want:    CompactionConfig{SafetyMarginTokens: 2048, SummaryMaxTokens: 256},
		},
		{
			name:    "sets only summary max tokens",
			initial: CompactionConfig{SafetyMarginTokens: 512, SummaryMaxTokens: 256},
			patch:   compactionPatch{SummaryMaxTokens: intPtr(128)},
			want:    CompactionConfig{SafetyMarginTokens: 512, SummaryMaxTokens: 128},
		},
		{
			name:    "nil patch leaves values untouched",
			initial: CompactionConfig{SafetyMarginTokens: 512, SummaryMaxTokens: 256},
			patch:   compactionPatch{},
			want:    CompactionConfig{SafetyMarginTokens: 512, SummaryMaxTokens: 256},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyCompactionPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyCompactionPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyRetryPatch(t *testing.T) {
	retry250ms := MustDuration("250ms")
	retry1s := MustDuration("1s")
	retry5s := MustDuration("5s")
	retry30s := MustDuration("30s")

	tests := []struct {
		name    string
		initial RetryConfig
		patch   retryPatch
		want    RetryConfig
	}{
		{
			name: "sets all fields",
			initial: RetryConfig{
				Enabled:        false,
				MaxAttempts:    1,
				InitialBackoff: retry250ms,
				MaxBackoff:     retry1s,
				RetryAfterMax:  retry5s,
			},
			patch: retryPatch{
				Enabled:        boolPtr(true),
				MaxAttempts:    intPtr(3),
				InitialBackoff: durationPtr(retry1s),
				MaxBackoff:     durationPtr(retry5s),
				RetryAfterMax:  durationPtr(retry30s),
			},
			want: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: retry1s,
				MaxBackoff:     retry5s,
				RetryAfterMax:  retry30s,
			},
		},
		{
			name: "nil fields leave values untouched",
			initial: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: retry250ms,
				MaxBackoff:     retry5s,
				RetryAfterMax:  retry30s,
			},
			patch: retryPatch{
				MaxAttempts: intPtr(9),
			},
			want: RetryConfig{
				Enabled:        true,
				MaxAttempts:    9,
				InitialBackoff: retry250ms,
				MaxBackoff:     retry5s,
				RetryAfterMax:  retry30s,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyRetryPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyRetryPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyModelPromptsPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial ModelPrompts
		patch   modelPromptsPatch
		want    ModelPrompts
	}{
		{
			name:    "sets both fields",
			initial: ModelPrompts{},
			patch: modelPromptsPatch{
				System:     stringPtr("You are a helpful assistant"),
				Compaction: stringPtr("Summarize the above"),
			},
			want: ModelPrompts{
				System:     "You are a helpful assistant",
				Compaction: "Summarize the above",
			},
		},
		{
			name:    "sets only system",
			initial: ModelPrompts{Compaction: "default compact"},
			patch:   modelPromptsPatch{System: stringPtr("Custom system")},
			want:    ModelPrompts{System: "Custom system", Compaction: "default compact"},
		},
		{
			name:    "sets only compaction",
			initial: ModelPrompts{System: "default system"},
			patch:   modelPromptsPatch{Compaction: stringPtr("Custom compact")},
			want:    ModelPrompts{System: "default system", Compaction: "Custom compact"},
		},
		{
			name:    "empty patch leaves values untouched",
			initial: ModelPrompts{System: "sys", Compaction: "comp"},
			patch:   modelPromptsPatch{},
			want:    ModelPrompts{System: "sys", Compaction: "comp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyModelPromptsPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyModelPromptsPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyLimitsPatch(t *testing.T) {
	timeout30s := MustDuration("30s")
	timeout120s := MustDuration("120s")

	tests := []struct {
		name    string
		initial LimitsConfig
		patch   limitsPatch
		want    LimitsConfig
	}{
		{
			name: "sets all scalar fields",
			initial: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: timeout30s,
				ToolOutputMaxBytes: 4096,
			},
			patch: limitsPatch{
				MaxTurns:           intPtr(50),
				MaxTokens:          intPtr(500000),
				ToolTimeoutDefault: durationPtr(timeout120s),
				ToolOutputMaxBytes: intPtr(65536),
			},
			want: LimitsConfig{
				MaxTurns:           50,
				MaxTokens:          500000,
				ToolTimeoutDefault: timeout120s,
				ToolOutputMaxBytes: 65536,
			},
		},
		{
			name: "creates ToolTimeouts map when nil and merges entries",
			initial: LimitsConfig{
				MaxTurns: 25,
			},
			patch: limitsPatch{
				ToolTimeouts: durationMapPtr(map[string]Duration{"bash": timeout120s, "read": timeout30s}),
			},
			want: LimitsConfig{
				MaxTurns:     25,
				ToolTimeouts: map[string]Duration{"bash": timeout120s, "read": timeout30s},
			},
		},
		{
			name: "merges ToolTimeouts into existing map",
			initial: LimitsConfig{
				ToolTimeouts: map[string]Duration{"bash": timeout30s, "write": timeout120s},
			},
			patch: limitsPatch{
				ToolTimeouts: durationMapPtr(map[string]Duration{"bash": timeout120s, "read": timeout30s}),
			},
			want: LimitsConfig{
				ToolTimeouts: map[string]Duration{
					"bash":  timeout120s,
					"write": timeout120s,
					"read":  timeout30s,
				},
			},
		},
		{
			name: "nil scalar fields leave existing values untouched",
			initial: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: timeout30s,
				ToolOutputMaxBytes: 4096,
			},
			patch: limitsPatch{
				MaxTurns: intPtr(99),
			},
			want: LimitsConfig{
				MaxTurns:           99,
				MaxTokens:          100000,
				ToolTimeoutDefault: timeout30s,
				ToolOutputMaxBytes: 4096,
			},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: LimitsConfig{MaxTurns: 10, MaxTokens: 100000},
			patch:   limitsPatch{},
			want:    LimitsConfig{MaxTurns: 10, MaxTokens: 100000},
		},
		{
			name: "ToolTimeouts nil patch field does not nil out existing map",
			initial: LimitsConfig{
				ToolTimeouts: map[string]Duration{"bash": timeout120s},
			},
			patch: limitsPatch{},
			want: LimitsConfig{
				ToolTimeouts: map[string]Duration{"bash": timeout120s},
			},
		},
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

func TestApplyApprovalPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial ApprovalConfig
		patch   approvalPatch
		want    ApprovalConfig
	}{
		{
			name:    "sets default mode",
			initial: ApprovalConfig{Default: ApprovalModePrompt},
			patch:   approvalPatch{Default: approvalModePtr(ApprovalModeAuto)},
			want:    ApprovalConfig{Default: ApprovalModeAuto},
		},
		{
			name:    "creates ToolOverrides map when nil and merges",
			initial: ApprovalConfig{Default: ApprovalModeAuto},
			patch: approvalPatch{
				ToolOverrides: approvalModeMapPtr(map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny)}),
			},
			want: ApprovalConfig{
				Default:       ApprovalModeAuto,
				ToolOverrides: map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny)},
			},
		},
		{
			name: "merges ToolOverrides into existing map",
			initial: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"read": approvalModePtr(ApprovalModeAuto)},
			},
			patch: approvalPatch{
				ToolOverrides: approvalModeMapPtr(map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny)}),
			},
			want: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"read": approvalModePtr(ApprovalModeAuto), "bash": approvalModePtr(ApprovalModeDeny)},
			},
		},
		{
			name: "tool_overrides patch replaces existing key",
			initial: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeAuto)},
			},
			patch: approvalPatch{
				ToolOverrides: approvalModeMapPtr(map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny)}),
			},
			want: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny)},
			},
		},
		{
			name: "nil default leaves existing default untouched",
			initial: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny)},
			},
			patch: approvalPatch{
				ToolOverrides: approvalModeMapPtr(map[string]*ApprovalMode{"read": approvalModePtr(ApprovalModeAuto)}),
			},
			want: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"bash": approvalModePtr(ApprovalModeDeny), "read": approvalModePtr(ApprovalModeAuto)},
			},
		},
		{
			name: "nil tool override is preserved",
			initial: ApprovalConfig{
				Default: ApprovalModePrompt,
			},
			patch: approvalPatch{
				ToolOverrides: approvalModeMapPtr(map[string]*ApprovalMode{"bash": nil}),
			},
			want: ApprovalConfig{
				Default:       ApprovalModePrompt,
				ToolOverrides: map[string]*ApprovalMode{"bash": nil},
			},
		},
		{
			name:    "empty patch leaves values untouched",
			initial: ApprovalConfig{Default: ApprovalModeDeny},
			patch:   approvalPatch{},
			want:    ApprovalConfig{Default: ApprovalModeDeny},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyApprovalPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyApprovalPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplySubAgentPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial SubAgentConfig
		patch   subAgentPatch
		want    SubAgentConfig
	}{
		{
			name: "sets all fields",
			initial: SubAgentConfig{
				Enabled: false, MaxTurns: 5, MaxTokens: 50000,
				AllowedTools: []string{"bash"}, AllowNesting: false, MaxConcurrent: 1,
			},
			patch: subAgentPatch{
				Enabled: boolPtr(true), MaxTurns: intPtr(10), MaxTokens: intPtr(100000),
				AllowedTools: stringSlicePtr("read", "write", "bash"),
				AllowNesting: boolPtr(true), MaxConcurrent: intPtr(3),
			},
			want: SubAgentConfig{
				Enabled: true, MaxTurns: 10, MaxTokens: 100000,
				AllowedTools: []string{"read", "write", "bash"},
				AllowNesting: true, MaxConcurrent: 3,
			},
		},
		{
			name: "AllowedTools is a copy not sharing backing array",
			initial: SubAgentConfig{
				AllowedTools: []string{"bash"},
			},
			patch: subAgentPatch{
				AllowedTools: stringSlicePtr("read", "write"),
			},
			want: SubAgentConfig{
				AllowedTools: []string{"read", "write"},
			},
		},
		{
			name: "nil fields leave existing values untouched",
			initial: SubAgentConfig{
				Enabled: true, MaxTurns: 10, MaxTokens: 100000,
				AllowedTools: []string{"bash"}, AllowNesting: true, MaxConcurrent: 3,
			},
			patch: subAgentPatch{
				MaxTurns: intPtr(25),
			},
			want: SubAgentConfig{
				Enabled: true, MaxTurns: 25, MaxTokens: 100000,
				AllowedTools: []string{"bash"}, AllowNesting: true, MaxConcurrent: 3,
			},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: SubAgentConfig{Enabled: true, MaxTurns: 5},
			patch:   subAgentPatch{},
			want:    SubAgentConfig{Enabled: true, MaxTurns: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applySubAgentPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applySubAgentPatch() = %#v, want %#v", dst, tt.want)
			}
			// Verify AllowedTools slice is not sharing backing array when patch provided
			if tt.patch.AllowedTools != nil && len(*tt.patch.AllowedTools) > 0 && len(dst.AllowedTools) > 0 {
				src := *tt.patch.AllowedTools
				if len(src) > 0 && len(dst.AllowedTools) > 0 && &src[0] == &dst.AllowedTools[0] {
					t.Fatal("AllowedTools shares backing array with patch source")
				}
			}
		})
	}
}

func TestApplyToolPatch(t *testing.T) {
	timeout5s := MustDuration("5s")
	timeout30s := MustDuration("30s")

	tests := []struct {
		name    string
		initial ToolConfig
		patch   toolPatch
		want    ToolConfig
	}{
		{
			name: "sets all scalar fields",
			initial: ToolConfig{
				Exec: "old", Subcommand: "old-cmd", Description: "old desc",
				Timeout: timeout5s, Approval: ApprovalModeAuto,
			},
			patch: toolPatch{
				Exec: stringPtr("new"), Subcommand: stringPtr("new-cmd"),
				Description: stringPtr("new desc"),
				Timeout:     durationPtr(timeout30s),
				Approval:    approvalModePtr(ApprovalModeDeny),
			},
			want: ToolConfig{
				Exec: "new", Subcommand: "new-cmd", Description: "new desc",
				Timeout: timeout30s, Approval: ApprovalModeDeny,
			},
		},
		{
			name:    "copies Parameters map",
			initial: ToolConfig{},
			patch: toolPatch{
				Parameters: stringAnyMapPtr(map[string]any{"key": "val", "count": 42}),
			},
			want: ToolConfig{
				Parameters: map[string]any{"key": "val", "count": 42},
			},
		},
		{
			name:    "copies Constraints map",
			initial: ToolConfig{},
			patch: toolPatch{
				Constraints: stringAnyMapPtr(map[string]any{"max-size": "1MB", "timeout": "10s"}),
			},
			want: ToolConfig{
				Constraints: map[string]any{"max-size": "1MB", "timeout": "10s"},
			},
		},
		{
			name:    "Parameters is a copy not sharing backing map",
			initial: ToolConfig{},
			patch: toolPatch{
				Parameters: stringAnyMapPtr(map[string]any{"key": "val"}),
			},
			want: ToolConfig{
				Parameters: map[string]any{"key": "val"},
			},
		},
		{
			name:    "Constraints is a copy not sharing backing map",
			initial: ToolConfig{},
			patch: toolPatch{
				Constraints: stringAnyMapPtr(map[string]any{"rule": "strict"}),
			},
			want: ToolConfig{
				Constraints: map[string]any{"rule": "strict"},
			},
		},
		{
			name: "nil fields leave existing values untouched",
			initial: ToolConfig{
				Exec: "bash", Subcommand: "run", Description: "run a command",
				Parameters: map[string]any{"existing": true}, Timeout: timeout30s,
				Approval: ApprovalModePrompt, Constraints: map[string]any{"old": "val"},
			},
			patch: toolPatch{
				Exec: stringPtr("python"),
			},
			want: ToolConfig{
				Exec: "python", Subcommand: "run", Description: "run a command",
				Parameters: map[string]any{"existing": true}, Timeout: timeout30s,
				Approval: ApprovalModePrompt, Constraints: map[string]any{"old": "val"},
			},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: ToolConfig{Exec: "bash", Timeout: timeout30s, Approval: ApprovalModeAuto},
			patch:   toolPatch{},
			want:    ToolConfig{Exec: "bash", Timeout: timeout30s, Approval: ApprovalModeAuto},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyToolPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyToolPatch() = %#v, want %#v", dst, tt.want)
			}
			// Verify Parameters does not share backing map
			if tt.patch.Parameters != nil {
				src := *tt.patch.Parameters
				if dst.Parameters != nil && len(src) > 0 {
					for k := range src {
						if dst.Parameters[k] != src[k] {
							break
						}
					}
				}
			}
			// Verify Constraints does not share backing map
			if tt.patch.Constraints != nil {
				src := *tt.patch.Constraints
				if dst.Constraints != nil && len(src) > 0 {
					for k := range src {
						if dst.Constraints[k] != src[k] {
							break
						}
					}
				}
			}
		})
	}
}

func TestApplyProjectContextPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial ProjectContextConfig
		patch   projectContextPatch
		want    ProjectContextConfig
	}{
		{
			name:    "sets MaxTokens",
			initial: ProjectContextConfig{MaxTokens: 1000},
			patch:   projectContextPatch{MaxTokens: intPtr(2000)},
			want:    ProjectContextConfig{MaxTokens: 2000},
		},
		{
			name: "copies ExtraFiles slice",
			initial: ProjectContextConfig{
				ExtraFiles: []string{"README.md"},
			},
			patch: projectContextPatch{
				ExtraFiles: stringSlicePtr("CONTRIBUTING.md", "CHANGELOG.md"),
			},
			want: ProjectContextConfig{
				ExtraFiles: []string{"CONTRIBUTING.md", "CHANGELOG.md"},
			},
		},
		{
			name: "copies IgnoreFiles slice",
			initial: ProjectContextConfig{
				IgnoreFiles: []string{"*.log"},
			},
			patch: projectContextPatch{
				IgnoreFiles: stringSlicePtr("*.tmp", "*.bak"),
			},
			want: ProjectContextConfig{
				IgnoreFiles: []string{"*.tmp", "*.bak"},
			},
		},
		{
			name: "ExtraFiles is a copy not sharing backing array",
			initial: ProjectContextConfig{
				ExtraFiles: []string{"old.md"},
			},
			patch: projectContextPatch{
				ExtraFiles: stringSlicePtr("new.md"),
			},
			want: ProjectContextConfig{
				ExtraFiles: []string{"new.md"},
			},
		},
		{
			name: "IgnoreFiles is a copy not sharing backing array",
			initial: ProjectContextConfig{
				IgnoreFiles: []string{"*.old"},
			},
			patch: projectContextPatch{
				IgnoreFiles: stringSlicePtr("*.new"),
			},
			want: ProjectContextConfig{
				IgnoreFiles: []string{"*.new"},
			},
		},
		{
			name: "nil fields leave existing values untouched",
			initial: ProjectContextConfig{
				MaxTokens: 3000, ExtraFiles: []string{"A.md"}, IgnoreFiles: []string{"*.a"},
			},
			patch: projectContextPatch{
				MaxTokens: intPtr(5000),
			},
			want: ProjectContextConfig{
				MaxTokens: 5000, ExtraFiles: []string{"A.md"}, IgnoreFiles: []string{"*.a"},
			},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: ProjectContextConfig{MaxTokens: 2000, ExtraFiles: []string{"docs.md"}},
			patch:   projectContextPatch{},
			want:    ProjectContextConfig{MaxTokens: 2000, ExtraFiles: []string{"docs.md"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyProjectContextPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyProjectContextPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyPathsPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial PathsConfig
		patch   pathsPatch
		want    PathsConfig
	}{
		{
			name:    "sets ProjectRootOnly",
			initial: PathsConfig{ProjectRootOnly: false},
			patch:   pathsPatch{ProjectRootOnly: boolPtr(true)},
			want:    PathsConfig{ProjectRootOnly: true},
		},
		{
			name:    "sets WritablePaths as a copy",
			initial: PathsConfig{WritablePaths: []string{"/tmp"}},
			patch:   pathsPatch{WritablePaths: stringSlicePtr("/home", "/data")},
			want:    PathsConfig{WritablePaths: []string{"/home", "/data"}},
		},
		{
			name:    "sets BlockedPaths as a copy",
			initial: PathsConfig{BlockedPaths: []string{"/etc"}},
			patch:   pathsPatch{BlockedPaths: stringSlicePtr("/proc", "/sys")},
			want:    PathsConfig{BlockedPaths: []string{"/proc", "/sys"}},
		},
		{
			name: "nil fields leave existing values untouched",
			initial: PathsConfig{
				ProjectRootOnly: true, WritablePaths: []string{"/a"}, BlockedPaths: []string{"/b"},
			},
			patch: pathsPatch{
				ProjectRootOnly: boolPtr(false),
			},
			want: PathsConfig{
				ProjectRootOnly: false, WritablePaths: []string{"/a"}, BlockedPaths: []string{"/b"},
			},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: PathsConfig{ProjectRootOnly: true},
			patch:   pathsPatch{},
			want:    PathsConfig{ProjectRootOnly: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyPathsPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyPathsPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyLoggingPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial LoggingConfig
		patch   loggingPatch
		want    LoggingConfig
	}{
		{
			name:    "sets all fields",
			initial: LoggingConfig{Enabled: false, Level: "info", File: "/dev/null", ThinkingChunk: false},
			patch: loggingPatch{
				Enabled: boolPtr(true), Level: stringPtr("debug"), File: stringPtr("steiner.log"), ThinkingChunk: boolPtr(true),
			},
			want: LoggingConfig{Enabled: true, Level: "debug", File: "steiner.log", ThinkingChunk: true},
		},
		{
			name:    "nil fields leave existing values untouched",
			initial: LoggingConfig{Enabled: true, Level: "warn", File: "old.log", ThinkingChunk: true},
			patch:   loggingPatch{Level: stringPtr("error")},
			want:    LoggingConfig{Enabled: true, Level: "error", File: "old.log", ThinkingChunk: true},
		},
		{
			name:    "empty patch leaves everything untouched",
			initial: LoggingConfig{Enabled: true, Level: "info", ThinkingChunk: true},
			patch:   loggingPatch{},
			want:    LoggingConfig{Enabled: true, Level: "info", ThinkingChunk: true},
		},
		{
			name:    "sets thinking_chunk",
			initial: LoggingConfig{ThinkingChunk: false},
			patch:   loggingPatch{ThinkingChunk: boolPtr(true)},
			want:    LoggingConfig{ThinkingChunk: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyLoggingPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyLoggingPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestApplyPatch(t *testing.T) {
	timeout30s := MustDuration("30s")
	timeout120s := MustDuration("120s")

	tests := []struct {
		name  string
		cfg   Config
		patch configPatch
		want  Config
	}{
		{
			name: "applies debug visibility flag",
			cfg: Config{
				Debug: DebugConfig{ShowInternalScaffoldInference: false},
			},
			patch: configPatch{
				Debug: debugPatchPtr(debugPatch{ShowInternalScaffoldInference: boolPtr(true)}),
			},
			want: Config{
				Debug: DebugConfig{ShowInternalScaffoldInference: true},
			},
		},
		{
			name: "nil sections do nothing",
			cfg: Config{
				Model: ModelConfig{Type: "openai_compat", Model: "default", BaseURL: "http://localhost:11434/v1", MaxCompletionTokens: 4096, ContextSize: 16384},
			},
			patch: configPatch{},
			want: Config{
				Model: ModelConfig{Type: "openai_compat", Model: "default", BaseURL: "http://localhost:11434/v1", MaxCompletionTokens: 4096, ContextSize: 16384},
			},
		},
		{
			name: "applies scheduler, model, and limits in one call",
			cfg: Config{
				Scheduler: SchedulerConfig{Parallelism: 1},
				Model:     ModelConfig{Type: "ollama", Model: "old", BaseURL: "http://old:11434/v1", MaxCompletionTokens: 4096, ContextSize: 16384},
				Limits:    LimitsConfig{MaxTurns: 10},
			},
			patch: configPatch{
				Scheduler: &schedulerPatch{Parallelism: intPtr(4)},
				Model:     &modelPatch{Model: stringPtr("new"), BaseURL: stringPtr("http://new:11434/v1")},
				Limits:    &limitsPatch{MaxTurns: intPtr(50)},
			},
			want: Config{
				Scheduler: SchedulerConfig{Parallelism: 4},
				Model:     ModelConfig{Type: "ollama", Model: "new", BaseURL: "http://new:11434/v1", MaxCompletionTokens: 4096, ContextSize: 16384},
				Limits:    LimitsConfig{MaxTurns: 50},
			},
		},
		{
			name: "creates Models map if nil and applies model patches",
			cfg: Config{
				Models: nil,
			},
			patch: configPatch{
				Models: modelPatchMapPtr(map[string]modelPatch{
					"default": {
						Type:                stringPtr("openai_compat"),
						BaseURL:             stringPtr("http://localhost:11434/v1"),
						Model:               stringPtr("qwen"),
						MaxCompletionTokens: intPtr(8192),
						ContextSize:         intPtr(32768),
					},
				}),
			},
			want: Config{
				Models: map[string]ModelConfig{
					"default": {
						Type:                "openai_compat",
						BaseURL:             "http://localhost:11434/v1",
						Model:               "qwen",
						MaxCompletionTokens: 8192,
						ContextSize:         32768,
					},
				},
			},
		},
		{
			name: "merges model patches into existing Models",
			cfg: Config{
				Models: map[string]ModelConfig{
					"existing": {
						Type:                "ollama",
						BaseURL:             "http://existing:11434/v1",
						Model:               "existing-model",
						MaxCompletionTokens: 4096,
						ContextSize:         16384,
					},
				},
			},
			patch: configPatch{
				Models: modelPatchMapPtr(map[string]modelPatch{
					"existing": {
						MaxCompletionTokens: intPtr(8192),
					},
					"new": {
						Type:                stringPtr("openai_compat"),
						BaseURL:             stringPtr("http://new:11434/v1"),
						Model:               stringPtr("new-model"),
						MaxCompletionTokens: intPtr(2048),
						ContextSize:         intPtr(8192),
					},
				}),
			},
			want: Config{
				Models: map[string]ModelConfig{
					"existing": {
						Type:                "ollama",
						BaseURL:             "http://existing:11434/v1",
						Model:               "existing-model",
						MaxCompletionTokens: 8192,
						ContextSize:         16384,
					},
					"new": {
						Type:                "openai_compat",
						BaseURL:             "http://new:11434/v1",
						Model:               "new-model",
						MaxCompletionTokens: 2048,
						ContextSize:         8192,
					},
				},
			},
		},
		{
			name: "creates Tools map if nil and applies tool patches",
			cfg: Config{
				Tools: nil,
			},
			patch: configPatch{
				Tools: toolPatchMapPtr(map[string]toolPatch{
					"bash": {
						Exec:     stringPtr("bash"),
						Timeout:  durationPtr(timeout30s),
						Approval: approvalModePtr(ApprovalModePrompt),
					},
				}),
			},
			want: Config{
				Tools: map[string]ToolConfig{
					"bash": {
						Exec:     "bash",
						Timeout:  timeout30s,
						Approval: ApprovalModePrompt,
					},
				},
			},
		},
		{
			name: "merges tool patches into existing Tools",
			cfg: Config{
				Tools: map[string]ToolConfig{
					"read": {
						Exec:     "cat",
						Timeout:  timeout30s,
						Approval: ApprovalModeAuto,
					},
				},
			},
			patch: configPatch{
				Tools: toolPatchMapPtr(map[string]toolPatch{
					"read": {
						Timeout: durationPtr(timeout120s),
					},
					"write": {
						Exec:     stringPtr("tee"),
						Timeout:  durationPtr(timeout30s),
						Approval: approvalModePtr(ApprovalModeDeny),
					},
				}),
			},
			want: Config{
				Tools: map[string]ToolConfig{
					"read": {
						Exec:     "cat",
						Timeout:  timeout120s,
						Approval: ApprovalModeAuto,
					},
					"write": {
						Exec:     "tee",
						Timeout:  timeout30s,
						Approval: ApprovalModeDeny,
					},
				},
			},
		},
		{
			name: "applies all config sections",
			cfg:  Config{},
			patch: configPatch{
				Scheduler:      &schedulerPatch{Parallelism: intPtr(2)},
				Model:          &modelPatch{Model: stringPtr("default"), Type: stringPtr("openai_compat"), BaseURL: stringPtr("http://localhost:11434/v1"), MaxCompletionTokens: intPtr(4096), ContextSize: intPtr(16384)},
				Limits:         &limitsPatch{MaxTurns: intPtr(50), MaxTokens: intPtr(500000)},
				Approval:       &approvalPatch{Default: approvalModePtr(ApprovalModeAuto)},
				SubAgent:       &subAgentPatch{Enabled: boolPtr(false)},
				ProjectContext: &projectContextPatch{MaxTokens: intPtr(2000), ExtraFiles: stringSlicePtr("docs.md")},
				Paths:          &pathsPatch{ProjectRootOnly: boolPtr(true)},
				Logging:        &loggingPatch{Level: stringPtr("info"), File: stringPtr("steiner.log")},
				Debug:          debugPatchPtr(debugPatch{ShowInternalScaffoldInference: boolPtr(true)}),
			},
			want: Config{
				Scheduler: SchedulerConfig{Parallelism: 2},
				Model:     ModelConfig{Type: "openai_compat", BaseURL: "http://localhost:11434/v1", Model: "default", MaxCompletionTokens: 4096, ContextSize: 16384},
				Limits:    LimitsConfig{MaxTurns: 50, MaxTokens: 500000},
				Approval:  ApprovalConfig{Default: ApprovalModeAuto},
				SubAgent:  SubAgentConfig{Enabled: false},
				ProjectContext: ProjectContextConfig{
					MaxTokens:  2000,
					ExtraFiles: []string{"docs.md"},
				},
				Paths:   PathsConfig{ProjectRootOnly: true},
				Logging: LoggingConfig{Level: "info", File: "steiner.log"},
				Debug:   DebugConfig{ShowInternalScaffoldInference: true},
			},
		},
		{
			name: "applies retry patch to singleton model and models map",
			cfg: Config{
				Model: ModelConfig{
					Retry: RetryConfig{
						Enabled:        true,
						MaxAttempts:    3,
						InitialBackoff: MustDuration("250ms"),
						MaxBackoff:     MustDuration("5s"),
						RetryAfterMax:  MustDuration("30s"),
					},
				},
				Models: map[string]ModelConfig{
					"default": {
						Retry: RetryConfig{
							Enabled:        true,
							MaxAttempts:    3,
							InitialBackoff: MustDuration("250ms"),
							MaxBackoff:     MustDuration("5s"),
							RetryAfterMax:  MustDuration("30s"),
						},
					},
				},
			},
			patch: configPatch{
				Model: &modelPatch{
					Retry: &retryPatch{
						Enabled:        boolPtr(false),
						MaxAttempts:    intPtr(8),
						InitialBackoff: durationPtr(MustDuration("1s")),
						MaxBackoff:     durationPtr(MustDuration("10s")),
						RetryAfterMax:  durationPtr(MustDuration("1m")),
					},
				},
				Models: modelPatchMapPtr(map[string]modelPatch{
					"default": {
						Retry: &retryPatch{
							Enabled:        boolPtr(false),
							MaxAttempts:    intPtr(4),
							InitialBackoff: durationPtr(MustDuration("500ms")),
							MaxBackoff:     durationPtr(MustDuration("4s")),
							RetryAfterMax:  durationPtr(MustDuration("45s")),
						},
					},
				}),
			},
			want: Config{
				Model: ModelConfig{
					Retry: RetryConfig{
						Enabled:        false,
						MaxAttempts:    8,
						InitialBackoff: MustDuration("1s"),
						MaxBackoff:     MustDuration("10s"),
						RetryAfterMax:  MustDuration("1m"),
					},
				},
				Models: map[string]ModelConfig{
					"default": {
						Retry: RetryConfig{
							Enabled:        false,
							MaxAttempts:    4,
							InitialBackoff: MustDuration("500ms"),
							MaxBackoff:     MustDuration("4s"),
							RetryAfterMax:  MustDuration("45s"),
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyPatch(&tt.cfg, tt.patch)
			if !reflect.DeepEqual(tt.cfg, tt.want) {
				t.Fatalf("applyPatch() = %#v, want %#v", tt.cfg, tt.want)
			}
		})
	}
}
