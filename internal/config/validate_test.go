package config

import (
	"strings"
	"testing"
)

func validBase() Config {
	retry := RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: MustDuration("250ms"),
		MaxBackoff:     MustDuration("5s"),
		RetryAfterMax:  MustDuration("30s"),
	}
	return Config{
		Scheduler: SchedulerConfig{Parallelism: 1},
		Model: ModelConfig{
			Type:                "openai_compat",
			BaseURL:             "http://localhost:11434/v1",
			Model:               "qwen3-35b-a3b",
			MaxCompletionTokens: 8192,
			ContextSize:         32768,
			Retry:               retry,
			Compaction: CompactionConfig{
				SafetyMarginTokens: 2048,
				SummaryMaxTokens:   1024,
			},
		},
		Models: map[string]ModelConfig{
			"default": {
				Type:                "openai_compat",
				BaseURL:             "http://localhost:11434/v1",
				Model:               "qwen3-35b-a3b",
				MaxCompletionTokens: 8192,
				ContextSize:         32768,
				Retry:               retry,
				Compaction: CompactionConfig{
					SafetyMarginTokens: 2048,
					SummaryMaxTokens:   1024,
				},
			},
		},
		Limits: LimitsConfig{
			MaxTurns:           50,
			MaxTokens:          500000,
			ToolTimeoutDefault: MustDuration("30s"),
			ToolTimeouts: map[string]Duration{
				"bash": MustDuration("120s"),
			},
			ToolOutputMaxBytes: 65536,
		},
		Approval: ApprovalConfig{
			Default:       ApprovalModeAuto,
			ToolOverrides: map[string]*ApprovalMode{},
		},
		SubAgent: SubAgentConfig{Enabled: false},
		Tools:    map[string]ToolConfig{},
		ProjectContext: ProjectContextConfig{
			MaxTokens: 2000,
		},
		Logging: LoggingConfig{
			Level: "info",
			File:  "steiner.log",
		},
		ContextManagement: ContextManagementConfig{
			Mode:               ContextModeNaive,
			CompactionStrategy: CompactionStrategyDrop,
			MaskingWindowTurns: 5,
			ReadAnnotations:    true,
			ScratchpadMode:     ScratchpadModeScaffoldOnly,
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "valid", cfg: validBase(), wantErr: ""},
		{
			name: "invalid scratchpad mode",
			cfg: func() Config {
				c := validBase()
				c.ContextManagement.ScratchpadMode = "bogus"
				return c
			}(),
			wantErr: `context_management.scratchpad_mode "bogus" is not supported`,
		},

		// 2. Empty model fields, bad parallelism, missing models
		{
			name: "empty model type",
			cfg: func() Config {
				c := validBase()
				c.Model.Type = ""
				return c
			}(),
			wantErr: "model.type is required",
		},
		{
			name: "empty model base_url",
			cfg: func() Config {
				c := validBase()
				c.Model.BaseURL = ""
				return c
			}(),
			wantErr: "model.base_url is required",
		},
		{
			name: "bad parallelism",
			cfg: func() Config {
				c := validBase()
				c.Scheduler.Parallelism = 0
				return c
			}(),
			wantErr: "scheduler.parallelism must be at least 1",
		},
		{
			name: "missing models",
			cfg: func() Config {
				c := validBase()
				c.Models = nil
				return c
			}(),
			wantErr: "models is required",
		},

		// 3. Model unsupported type, empty base_url, empty model, bad token counts
		{
			name: "unsupported model type",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Type = "anthropic"
				c.Models["default"] = m
				return c
			}(),
			wantErr: `not supported`,
		},
		{
			name: "empty model type",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Type = ""
				c.Models["default"] = m
				return c
			}(),
			wantErr: `type is required`,
		},
		{
			name: "empty base_url",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.BaseURL = ""
				c.Models["default"] = m
				return c
			}(),
			wantErr: `base_url is required`,
		},
		{
			name: "empty model field",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Model = ""
				c.Models["default"] = m
				return c
			}(),
			wantErr: `model is required`,
		},
		{
			name: "zero max_completion_tokens",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.MaxCompletionTokens = 0
				c.Models["default"] = m
				return c
			}(),
			wantErr: `max_completion_tokens must be at least 1`,
		},
		{
			name: "zero context_size",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.ContextSize = 0
				c.Models["default"] = m
				return c
			}(),
			wantErr: `context_size must be at least 1`,
		},

		// 4. Compaction validation
		{
			name: "negative safety_margin_tokens",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Compaction.SafetyMarginTokens = -1
				c.Models["default"] = m
				return c
			}(),
			wantErr: `safety_margin_tokens must be at least 0`,
		},
		{
			name: "zero summary_max_tokens",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Compaction.SummaryMaxTokens = 0
				c.Models["default"] = m
				return c
			}(),
			wantErr: `summary_max_tokens must be at least 1`,
		},
		{
			name: "summary exceeds completion tokens",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.MaxCompletionTokens = 100
				m.Compaction.SummaryMaxTokens = 500
				c.Models["default"] = m
				return c
			}(),
			wantErr: `summary_max_tokens must be less than or equal to models["default"].max_completion_tokens`,
		},

		// 5. Limits with zero/negative values
		{
			name: "negative max_turns",
			cfg: func() Config {
				c := validBase()
				c.Limits.MaxTurns = -1
				return c
			}(),
			wantErr: `max_turns must be non-negative`,
		},
		{
			name: "zero max_tokens",
			cfg: func() Config {
				c := validBase()
				c.Limits.MaxTokens = 0
				return c
			}(),
			wantErr: `max_tokens must be at least 1`,
		},
		{
			name: "zero tool_timeout_default",
			cfg: func() Config {
				c := validBase()
				c.Limits.ToolTimeoutDefault = Duration{}
				return c
			}(),
			wantErr: `tool_timeout_default must be greater than zero`,
		},
		{
			name: "zero tool_output_max_bytes",
			cfg: func() Config {
				c := validBase()
				c.Limits.ToolOutputMaxBytes = 0
				return c
			}(),
			wantErr: `tool_output_max_bytes must be at least 1`,
		},
		{
			name: "empty tool timeout name",
			cfg: func() Config {
				c := validBase()
				c.Limits.ToolTimeouts[""] = MustDuration("5s")
				return c
			}(),
			wantErr: `tool_timeouts contains an empty tool name`,
		},
		{
			name: "zero tool timeout",
			cfg: func() Config {
				c := validBase()
				c.Limits.ToolTimeouts["test"] = Duration{}
				return c
			}(),
			wantErr: `must be greater than zero`,
		},

		// 6. Retry validation
		{
			name: "zero retry max_attempts",
			cfg: func() Config {
				c := validBase()
				c.Model.Retry.MaxAttempts = 0
				return c
			}(),
			wantErr: `model.retry.max_attempts must be at least 1`,
		},
		{
			name: "zero retry duration",
			cfg: func() Config {
				c := validBase()
				c.Model.Retry.InitialBackoff = Duration{}
				return c
			}(),
			wantErr: `model.retry.initial_backoff must be greater than zero`,
		},
		{
			name: "retry max_backoff less than initial_backoff",
			cfg: func() Config {
				c := validBase()
				c.Model.Retry.InitialBackoff = MustDuration("5s")
				c.Model.Retry.MaxBackoff = MustDuration("1s")
				return c
			}(),
			wantErr: `model.retry.max_backoff must be greater than or equal to model.retry.initial_backoff`,
		},
		{
			name: "alias retry validation reports alias path",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Retry.RetryAfterMax = Duration{}
				c.Models["fast"] = m
				return c
			}(),
			wantErr: `models["fast"].retry.retry_after_max must be greater than zero`,
		},

		// 7. Approval mode invalid or empty
		{
			name: "invalid approval default",
			cfg: func() Config {
				c := validBase()
				c.Approval.Default = "invalid"
				return c
			}(),
			wantErr: `not supported`,
		},
		{
			name: "empty approval default",
			cfg: func() Config {
				c := validBase()
				c.Approval.Default = ""
				return c
			}(),
			wantErr: `is required`,
		},
		{
			name: "invalid approval override",
			cfg: func() Config {
				c := validBase()
				c.Approval.ToolOverrides["test"] = approvalModePtr("invalid")
				return c
			}(),
			wantErr: `not supported`,
		},
		{
			name: "empty approval override",
			cfg: func() Config {
				c := validBase()
				c.Approval.ToolOverrides["test"] = approvalModePtr("")
				return c
			}(),
			wantErr: `is required`,
		},
		{
			name: "nil approval override inherits default",
			cfg: func() Config {
				c := validBase()
				c.Approval.ToolOverrides["bash"] = nil
				return c
			}(),
			wantErr: ``,
		},

		// 8. Sub-agent enabled with bad limits
		{
			name: "subagent zero max_turns",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.Enabled = true
				c.SubAgent.MaxTurns = 0
				return c
			}(),
			wantErr: `max_turns must be at least 1 when enabled`,
		},
		{
			name: "subagent zero max_tokens",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.Enabled = true
				c.SubAgent.MaxTokens = 0
				return c
			}(),
			wantErr: `max_tokens must be at least 1 when enabled`,
		},

		// 9. Project context max tokens < 1
		{
			name: "zero project_context max_tokens",
			cfg: func() Config {
				c := validBase()
				c.ProjectContext.MaxTokens = 0
				return c
			}(),
			wantErr: `max_tokens must be at least 1`,
		},

		// 10. Logging level invalid or empty file path
		{
			name: "invalid logging level",
			cfg: func() Config {
				c := validBase()
				c.Logging.Level = "nope"
				return c
			}(),
			wantErr: `not supported`,
		},
		{
			name: "empty logging file",
			cfg: func() Config {
				c := validBase()
				c.Logging.File = ""
				return c
			}(),
			wantErr: `logging.file is required`,
		},

		// 11. Tool validation failures
		{
			name: "tool empty name",
			cfg: func() Config {
				c := validBase()
				c.Tools[""] = ToolConfig{Exec: "bar", Timeout: MustDuration("5s")}
				return c
			}(),
			wantErr: `tools contains an empty tool name`,
		},
		{
			name: "tool empty exec",
			cfg: func() Config {
				c := validBase()
				c.Tools["foo"] = ToolConfig{Timeout: MustDuration("5s")}
				return c
			}(),
			wantErr: `exec is required`,
		},
		{
			name: "tool zero timeout",
			cfg: func() Config {
				c := validBase()
				c.Tools["foo"] = ToolConfig{Exec: "bar"}
				return c
			}(),
			wantErr: `timeout must be greater than zero`,
		},
		{
			name: "tool invalid approval",
			cfg: func() Config {
				c := validBase()
				c.Tools["foo"] = ToolConfig{Exec: "bar", Timeout: MustDuration("5s"), Approval: "invalid"}
				return c
			}(),
			wantErr: `not supported`,
		},

		// Empty model alias
		{
			name: "empty model alias",
			cfg: func() Config {
				c := validBase()
				c.Models[""] = ModelConfig{
					Type:                "openai_compat",
					BaseURL:             "http://localhost:11434/v1",
					Model:               "alias-model",
					MaxCompletionTokens: 100,
					ContextSize:         100,
				}
				return c
			}(),
			wantErr: `models contains an empty alias`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
