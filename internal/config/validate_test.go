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
		Scheduler:    SchedulerConfig{Parallelism: 1},
		DefaultModel: "default",
		Providers: map[string]ProviderConfig{
			"local": {
				Type:    ProviderTypeOpenAICompat,
				BaseURL: "http://localhost:11434/v1",
			},
		},
		Models: map[string]ModelConfig{
			"default": {
				Provider: "local",
				ID:       "qwen3-35b-a3b",
				Retry:    retry,
				Advanced: AdvancedConfig{
					Limits: AdvancedLimitsConfig{
						ContextWindow:   32768,
						MaxOutputTokens: 8192,
					},
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
			ReadAnnotations: true,
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
		// Missing default_model, providers, models
		{
			name: "missing default_model",
			cfg: func() Config {
				c := validBase()
				c.DefaultModel = ""
				return c
			}(),
			wantErr: "default_model is required",
		},
		{
			name: "default_model not in models",
			cfg: func() Config {
				c := validBase()
				c.DefaultModel = "missing"
				return c
			}(),
			wantErr: "default_model",
		},
		{
			name: "missing providers",
			cfg: func() Config {
				c := validBase()
				c.Providers = nil
				return c
			}(),
			wantErr: "providers is required",
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

		// Provider validation
		{
			name: "provider missing type",
			cfg: func() Config {
				c := validBase()
				c.Providers["local"] = ProviderConfig{
					Type:    "",
					BaseURL: "http://localhost:11434/v1",
				}
				return c
			}(),
			wantErr: "providers[\"local\"].type is required",
		},
		{
			name: "provider unsupported type",
			cfg: func() Config {
				c := validBase()
				c.Providers["local"] = ProviderConfig{
					Type:    "unsupported",
					BaseURL: "http://localhost:11434/v1",
				}
				return c
			}(),
			wantErr: "providers[\"local\"].type",
		},
		{
			name: "provider missing base_url",
			cfg: func() Config {
				c := validBase()
				c.Providers["local"] = ProviderConfig{
					Type: ProviderTypeOpenAICompat,
				}
				return c
			}(),
			wantErr: "providers[\"local\"].base_url is required",
		},
		{
			name: "openrouter without base_url validates with api_key_env",
			cfg: func() Config {
				c := validBase()
				c.Providers["local"] = ProviderConfig{
					Type:      ProviderTypeOpenRouter,
					APIKeyEnv: "OPENROUTER_API_KEY",
				}
				return c
			}(),
			wantErr: "",
		},
		{
			name: "credentialed provider missing api key",
			cfg: func() Config {
				c := validBase()
				c.Providers["local"] = ProviderConfig{
					Type: ProviderTypeOpenRouter,
				}
				return c
			}(),
			wantErr: `providers["local"] must set api_key or api_key_env`,
		},
		{
			name: "empty provider alias",
			cfg: func() Config {
				c := validBase()
				c.Providers[""] = ProviderConfig{
					Type:    ProviderTypeOpenAICompat,
					BaseURL: "http://localhost:11434/v1",
				}
				return c
			}(),
			wantErr: "providers contains an empty alias",
		},

		// Model validation
		{
			name: "model missing provider",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Provider = ""
				c.Models["default"] = m
				return c
			}(),
			wantErr: "models[\"default\"].provider is required",
		},
		{
			name: "model provider not in providers",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Provider = "missing"
				c.Models["default"] = m
				return c
			}(),
			wantErr: "models[\"default\"].provider",
		},
		{
			name: "model missing id",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.ID = ""
				c.Models["default"] = m
				return c
			}(),
			wantErr: "models[\"default\"].id is required",
		},
		{
			name: "empty model alias",
			cfg: func() Config {
				c := validBase()
				c.Models[""] = ModelConfig{
					Provider: "local",
					ID:       "test-model",
				}
				return c
			}(),
			wantErr: "models contains an empty alias",
		},

		// Limits validation
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

		// Retry validation
		{
			name: "zero retry max_attempts",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Retry.MaxAttempts = 0
				c.Models["default"] = m
				return c
			}(),
			wantErr: `models["default"].retry.max_attempts must be at least 1`,
		},
		{
			name: "zero retry duration",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Retry.InitialBackoff = Duration{}
				c.Models["default"] = m
				return c
			}(),
			wantErr: `models["default"].retry.initial_backoff must be greater than zero`,
		},
		{
			name: "retry max_backoff less than initial_backoff",
			cfg: func() Config {
				c := validBase()
				m := c.Models["default"]
				m.Retry.InitialBackoff = MustDuration("5s")
				m.Retry.MaxBackoff = MustDuration("1s")
				c.Models["default"] = m
				return c
			}(),
			wantErr: `models["default"].retry.max_backoff must be greater than or equal to`,
		},

		// Scheduler validation
		{
			name: "bad parallelism",
			cfg: func() Config {
				c := validBase()
				c.Scheduler.Parallelism = 0
				return c
			}(),
			wantErr: "scheduler.parallelism must be at least 1",
		},

		// Approval mode invalid or empty
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

		// Sub-agent enabled with bad limits
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

		// Project context max tokens < 1
		{
			name: "zero project_context max_tokens",
			cfg: func() Config {
				c := validBase()
				c.ProjectContext.MaxTokens = 0
				return c
			}(),
			wantErr: `max_tokens must be at least 1`,
		},

		// Logging level invalid or empty file path
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

		// Tool validation failures
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

		// Sub-agent Agents map validation
		{
			name: "subagent unknown agent type rejected",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.Agents = map[string]AgentConfig{
					"bogus": {Model: "some-model"},
				}
				return c
			}(),
			wantErr: `sub_agent.agents contains unknown agent type "bogus"`,
		},
		{
			name: "subagent known agent types accepted",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.Agents = map[string]AgentConfig{
					"explore":  {Model: "model-a"},
					"research": {Model: "model-b"},
					"code":     {Model: "model-c"},
					"plan":     {Model: "model-d"},
					"verify":   {Model: "model-e"},
				}
				return c
			}(),
			wantErr: ``,
		},
		{
			name: "subagent agents validated even when disabled",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.Enabled = false
				c.SubAgent.Agents = map[string]AgentConfig{
					"unknown": {Model: "some-model"},
				}
				return c
			}(),
			wantErr: `sub_agent.agents contains unknown agent type "unknown"`,
		},

		// Search validation
		{
			name: "search disabled (empty backend)",
			cfg: func() Config {
				c := validBase()
				c.Search = SearchConfig{Backend: ""}
				return c
			}(),
			wantErr: "",
		},
		{
			name: "search backend unknown",
			cfg: func() Config {
				c := validBase()
				c.Search = SearchConfig{Backend: "unknown"}
				return c
			}(),
			wantErr: `search.backend "unknown" is not supported`,
		},
		{
			name: "search backend searxng without url",
			cfg: func() Config {
				c := validBase()
				c.Search = SearchConfig{Backend: "searxng", SearxngURL: ""}
				return c
			}(),
			wantErr: `search.backend is "searxng" but search.searxng_url is not set`,
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

func TestSearchConfigValidation(t *testing.T) {
	tests := []struct {
		name         string
		backend      string
		url          string
		googleCx     string
		googleAPIKey string
		kagiAPIKey   string
		braveAPIKey  string
		wantErr      string
	}{
		{
			name:         "google with env vars",
			backend:      "google",
			googleCx:     "test-cx",
			googleAPIKey: "test-key",
			wantErr:      "",
		},
		{
			name:         "google missing google_cx",
			backend:      "google",
			googleAPIKey: "test-key",
			wantErr:      "google_cx is not set",
		},
		{
			name:     "google missing google_api_key",
			backend:  "google",
			googleCx: "test-cx",
			wantErr:  "google_api_key is not set",
		},
		{
			name:       "kagi with env var",
			backend:    "kagi",
			kagiAPIKey: "test-key",
			wantErr:    "",
		},
		{
			name:    "kagi missing env var",
			backend: "kagi",
			wantErr: "kagi_api_key is not set",
		},
		{
			name:        "brave with env var",
			backend:     "brave",
			braveAPIKey: "test-key",
			wantErr:     "",
		},
		{
			name:    "brave missing env var",
			backend: "brave",
			wantErr: "brave_api_key is not set",
		},
		{
			name:    "searxng with url",
			backend: "searxng",
			url:     "http://localhost:8888",
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retry := RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("250ms"),
				MaxBackoff:     MustDuration("5s"),
				RetryAfterMax:  MustDuration("30s"),
			}
			cfg := Config{
				Scheduler:    SchedulerConfig{Parallelism: 1},
				DefaultModel: "default",
				Providers: map[string]ProviderConfig{
					"local": {
						Type:    ProviderTypeOpenAICompat,
						BaseURL: "http://localhost:11434/v1",
					},
				},
				Models: map[string]ModelConfig{
					"default": {
						Provider: "local",
						ID:       "qwen3-35b-a3b",
						Retry:    retry,
						Advanced: AdvancedConfig{
							Limits: AdvancedLimitsConfig{
								ContextWindow:   32768,
								MaxOutputTokens: 8192,
							},
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
					ReadAnnotations: true,
				},
				Search: SearchConfig{
					Backend:      tt.backend,
					SearxngURL:   tt.url,
					GoogleCx:     tt.googleCx,
					GoogleAPIKey: tt.googleAPIKey,
					KagiAPIKey:   tt.kagiAPIKey,
					BraveAPIKey:  tt.braveAPIKey,
				},
			}

			err := validate(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() error = nil, want substring %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func approvalModePtr(mode ApprovalMode) *ApprovalMode {
	return &mode
}
