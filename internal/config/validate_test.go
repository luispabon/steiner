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

		TUI: TUIConfig{FPS: 60},
		Models: ModelsConfig{
			Profiles: map[string]ModelProfile{
				"default": {DefaultModel: "default", defaultModelSet: true},
			},
			Definitions: map[string]ModelConfig{
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
		},
		Providers: map[string]ProviderConfig{
			"local": {
				Type:    ProviderTypeOpenAICompat,
				BaseURL: "http://localhost:11434/v1",
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
		SubAgent: SubAgentConfig{Enabled: false},
		Tools:    map[string]ToolConfig{},
		ProjectContext: ProjectContextConfig{
			MaxBytes: 8000,
		},
		Logging: LoggingConfig{
			Level: "info",
			File:  "steiner.log",
		},
		ContextManagement: ContextManagementConfig{
			ReadAnnotations: true,
		},
		Modes: ModesConfig{
			Default: ExecutionModeBuild,
		},
	}
}

func setDefaultModel(cfg *Config, value string) {
	profile := cfg.Models.Profiles["default"]
	profile.DefaultModel = value
	cfg.Models.Profiles["default"] = profile
}

func setDefaultSubAgents(cfg *Config, value map[string]string) {
	profile := cfg.Models.Profiles["default"]
	profile.SubAgents = value
	cfg.Models.Profiles["default"] = profile
}

func setDefaultOneShot(cfg *Config, value map[string]string) {
	profile := cfg.Models.Profiles["default"]
	profile.OneShot = value
	cfg.Models.Profiles["default"] = profile
}

func setDefaultWorkflowHandoff(cfg *Config, value map[string]string) {
	profile := cfg.Models.Profiles["default"]
	profile.WorkflowHandoff = value
	cfg.Models.Profiles["default"] = profile
}

func setDefaultAdvisor(cfg *Config) {
	profile := cfg.Models.Profiles["default"]
	profile.Advisor = "default"
	cfg.Models.Profiles["default"] = profile
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "valid", cfg: validBase(), wantErr: ""},
		// Missing models.default, providers, models.definitions
		{
			name: "missing models.default",
			cfg: func() Config {
				c := validBase()
				c.Models.Profiles["default"] = ModelProfile{}
				return c
			}(),
			wantErr: "models.profiles.default.default_model is required",
		},
		{
			name: "models.default not in models.definitions",
			cfg: func() Config {
				c := validBase()
				setDefaultModel(&c, "missing")
				return c
			}(),
			wantErr: "models.profiles",
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
				c.Models.Definitions = nil
				return c
			}(),
			wantErr: "models is required",
		},
		{
			name: "invalid model transport override",
			cfg: func() Config {
				c := validBase()
				c.Models.Definitions["default"] = ModelConfig{
					Provider: "local",
					ID:       "qwen3-35b-a3b",
					Retry:    c.Models.Definitions["default"].Retry,
					Advanced: AdvancedConfig{
						Limits: AdvancedLimitsConfig{
							ContextWindow:   32768,
							MaxOutputTokens: 8192,
						},
						Transport: ModelTransportType("invalid"),
					},
				}
				return c
			}(),
			wantErr: "models[\"default\"].advanced.transport \"invalid\" is not supported",
		},
		{
			name: "reasoning effort blank after trim",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Advanced.Reasoning = ReasoningConfig{Effort: "   "}
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].advanced.reasoning.effort must not be blank`,
		},
		{
			name: "reasoning supported_efforts contains blank value",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Advanced.Reasoning = ReasoningConfig{SupportedEfforts: []string{"low", "  "}}
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].advanced.reasoning.supported_efforts must not contain blank values`,
		},
		{
			name: "reasoning supported_efforts contains duplicate value",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Advanced.Reasoning = ReasoningConfig{SupportedEfforts: []string{"low", "low"}}
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].advanced.reasoning.supported_efforts contains duplicate value "low"`,
		},
		{
			name: "reasoning effort not present in supported_efforts",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Advanced.Reasoning = ReasoningConfig{
					Effort:           "xhigh",
					SupportedEfforts: []string{"low", "medium", "high"},
				}
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].advanced.reasoning.effort "xhigh" is not present in models["default"].advanced.reasoning.supported_efforts`,
		},
		{
			name: "reasoning effort matching supported_efforts is valid",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Advanced.Reasoning = ReasoningConfig{
					Effort:           "xhigh",
					SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
				}
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: "",
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
				m := c.Models.Definitions["default"]
				m.Provider = ""
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: "models[\"default\"].provider is required",
		},
		{
			name: "model provider not in providers",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Provider = "missing"
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: "models[\"default\"].provider",
		},
		{
			name: "model missing id",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.ID = ""
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: "models[\"default\"].id is required",
		},
		{
			name: "empty model alias",
			cfg: func() Config {
				c := validBase()
				c.Models.Definitions[""] = ModelConfig{
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
				m := c.Models.Definitions["default"]
				m.Retry.MaxAttempts = 0
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].retry.max_attempts must be at least 1`,
		},
		{
			name: "zero retry duration",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Retry.InitialBackoff = Duration{}
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].retry.initial_backoff must be greater than zero`,
		},
		{
			name: "retry max_backoff less than initial_backoff",
			cfg: func() Config {
				c := validBase()
				m := c.Models.Definitions["default"]
				m.Retry.InitialBackoff = MustDuration("5s")
				m.Retry.MaxBackoff = MustDuration("1s")
				c.Models.Definitions["default"] = m
				return c
			}(),
			wantErr: `models["default"].retry.max_backoff must be greater than or equal to`,
		},

		// Max parallel validation
		{
			name: "negative max_parallel",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.MaxParallel = -1
				return c
			}(),
			wantErr: "sub_agent.max_parallel must not be negative",
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

		// Project context max bytes < 1
		{
			name: "zero project_context max_bytes",
			cfg: func() Config {
				c := validBase()
				c.ProjectContext.MaxBytes = 0
				return c
			}(),
			wantErr: `max_bytes must be at least 1`,
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
		// models.profiles["default"].sub_agents map validation
		{
			name: "subagent unknown agent type rejected",
			cfg: func() Config {
				c := validBase()
				setDefaultSubAgents(&c, map[string]string{
					"bogus": "some-model",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].sub_agents contains unknown agent type "bogus"`,
		},
		{
			name: "subagent known agent types accepted",
			cfg: func() Config {
				c := validBase()
				c.Models.Definitions["alt-a"] = c.Models.Definitions["default"]
				c.Models.Definitions["alt-b"] = c.Models.Definitions["default"]
				setDefaultSubAgents(&c, map[string]string{
					"explore":      "alt-a",
					"research":     "default",
					"code":         "alt-b",
					"evaluate":     "alt-a",
					"sanity_check": "default",
					"review":       "alt-b",
					"vision":       "alt-a",
				})
				return c
			}(),
			wantErr: ``,
		},
		{
			name: "subagent unknown alias rejected",
			cfg: func() Config {
				c := validBase()
				setDefaultSubAgents(&c, map[string]string{
					"explore": "missing-model",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].sub_agents["explore"] "missing-model" is not defined in models.definitions`,
		},
		{
			name: "subagent empty alias accepted (disables agent)",
			cfg: func() Config {
				c := validBase()
				setDefaultSubAgents(&c, map[string]string{
					"vision": "",
				})
				return c
			}(),
			wantErr: "",
		},
		{
			name: "workflow handoff known aliases accepted",
			cfg: func() Config {
				c := validBase()
				c.Models.Definitions["alt-a"] = c.Models.Definitions["default"]
				setDefaultWorkflowHandoff(&c, map[string]string{
					"implement": "alt-a",
					"review":    "default",
				})
				return c
			}(),
			wantErr: ``,
		},
		{
			name: "workflow handoff unknown alias rejected",
			cfg: func() Config {
				c := validBase()
				setDefaultWorkflowHandoff(&c, map[string]string{
					"implement": "missing-model",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].workflow_handoff["implement"] "missing-model" is not defined in models.definitions`,
		},
		{
			name: "workflow handoff unknown destination rejected",
			cfg: func() Config {
				c := validBase()
				setDefaultWorkflowHandoff(&c, map[string]string{
					"plan": "default",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].workflow_handoff contains unknown destination "plan"`,
		},
		{
			name: "oneshot known aliases accepted",
			cfg: func() Config {
				c := validBase()
				c.Models.Definitions["alt-a"] = c.Models.Definitions["default"]
				c.Models.Definitions["alt-b"] = c.Models.Definitions["default"]
				setDefaultOneShot(&c, map[string]string{
					"plan":      "alt-a",
					"implement": "default",
					"review":    "alt-b",
				})
				return c
			}(),
			wantErr: ``,
		},
		{
			name: "oneshot unknown alias rejected",
			cfg: func() Config {
				c := validBase()
				setDefaultOneShot(&c, map[string]string{
					"plan": "missing-model",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].oneshot["plan"] "missing-model" is not defined in models.definitions`,
		},
		{
			name: "oneshot unknown phase rejected",
			cfg: func() Config {
				c := validBase()
				setDefaultOneShot(&c, map[string]string{
					"bogus": "default",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].oneshot contains unknown phase "bogus"`,
		},
		{
			name: "subagent agents validated even when disabled",
			cfg: func() Config {
				c := validBase()
				c.SubAgent.Enabled = false
				setDefaultSubAgents(&c, map[string]string{
					"unknown": "some-model",
				})
				return c
			}(),
			wantErr: `models.profiles["default"].sub_agents contains unknown agent type "unknown"`,
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

		// MCP validation
		{
			name: "mcp disabled (enabled false)",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{Enabled: false}
				return c
			}(),
			wantErr: "",
		},
		{
			name: "mcp with valid server",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{
					Enabled: true,
					Servers: map[string]MCPServerConfig{
						"example": {Enabled: true, Transport: "stdio", Command: "npx"},
					},
				}
				return c
			}(),
			wantErr: "",
		},
		{
			name: "mcp server with unsupported transport",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{
					Enabled: true,
					Servers: map[string]MCPServerConfig{
						"example": {Transport: "grpc"},
					},
				}
				return c
			}(),
			wantErr: `mcp.servers.example.transport: "grpc" is not supported`,
		},
		{
			name: "mcp server with empty command",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{
					Enabled: true,
					Servers: map[string]MCPServerConfig{
						"example": {Transport: "stdio"},
					},
				}
				return c
			}(),
			wantErr: "mcp.servers.example.command is required",
		},
		{
			name: "mcp server name with __ delimiter",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{
					Servers: map[string]MCPServerConfig{
						"a__b": {Transport: "stdio", Command: "npx"},
					},
				}
				return c
			}(),
			wantErr: `server name must not contain "__"`,
		},
		{
			name: "mcp server empty name",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{
					Servers: map[string]MCPServerConfig{
						"": {Transport: "stdio", Command: "npx"},
					},
				}
				return c
			}(),
			wantErr: "contains an empty server name",
		},
		{
			name: "mcp validation runs when enabled is false",
			cfg: func() Config {
				c := validBase()
				c.MCP = MCPConfig{
					Enabled: false,
					Servers: map[string]MCPServerConfig{
						"bad": {Transport: "grpc"},
					},
				}
				return c
			}(),
			wantErr: `mcp.servers.bad.transport: "grpc" is not supported`,
		},
		{
			name: "modes.default plan accepted",
			cfg: func() Config {
				c := validBase()
				c.Modes = ModesConfig{Default: ExecutionModePlan}
				return c
			}(),
			wantErr: ``,
		},
		{
			name: "modes.default build accepted",
			cfg: func() Config {
				c := validBase()
				c.Modes = ModesConfig{Default: ExecutionModeBuild}
				return c
			}(),
			wantErr: ``,
		},
		{
			name: "modes.default invalid value rejected",
			cfg: func() Config {
				c := validBase()
				c.Modes = ModesConfig{Default: ExecutionMode("invalid")}
				return c
			}(),
			wantErr: `modes.default "invalid" is not supported (valid: "plan", "build")`,
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

func TestValidateAdvisorConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "disabled advisor allows empty fields",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{}
			},
		},
		{
			name: "enabled advisor allows empty assignment",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{Enabled: true, MaxUsesPerRun: 1}
			},
		},
		{
			name: "enabled advisor allows empty assignment in named profile",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{Enabled: true, MaxUsesPerRun: 1}
				c.Models.Profiles["named"] = ModelProfile{DefaultModel: "default", advisorSet: true}
			},
		},
		{
			name: "invalid explicit advisor is rejected",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{Enabled: true, MaxUsesPerRun: 1}
				profile := c.Models.Profiles["default"]
				profile.Advisor = "missing-advisor"
				c.Models.Profiles["default"] = profile
			},
			wantErr: `models.profiles["default"].advisor "missing-advisor" is not defined in models.definitions or providers`,
		},
		{
			name: "enabled advisor requires max uses",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{Enabled: true}
				setDefaultAdvisor(c)
			},
			wantErr: "advisor.max_uses_per_run must be at least 1 when enabled",
		},
		{
			name: "advisor max tokens must be positive when set",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{
					Enabled:       true,
					MaxUsesPerRun: 1,
					MaxTokens:     intPtr(0),
				}
				setDefaultAdvisor(c)
			},
			wantErr: "advisor.max_tokens must be greater than zero when set",
		},
		{
			name: "advisor timeout must be positive when set",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{
					Enabled:       true,
					MaxUsesPerRun: 1,
					Timeout:       durationPtr(Duration{}),
				}
				setDefaultAdvisor(c)
			},
			wantErr: "advisor.timeout must be greater than zero when set",
		},
		{
			name: "advisor timeout rejected when advisor disabled",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{
					Enabled: false,
					Timeout: durationPtr(Duration{}),
				}
			},
			wantErr: "advisor.timeout must be greater than zero when set",
		},
		{
			name: "valid advisor config",
			mutate: func(c *Config) {
				c.Advisor = AdvisorConfig{
					Enabled:       true,
					MaxUsesPerRun: 2,
					MaxTokens:     intPtr(256),
					Timeout:       durationPtr(MustDuration("240s")),
				}
				setDefaultAdvisor(c)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			tt.mutate(&cfg)
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
			if got := err.Error(); !strings.Contains(got, tt.wantErr) {
				t.Fatalf("validate() error = %q, want substring %q", got, tt.wantErr)
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

				TUI: TUIConfig{FPS: 60},
				Models: ModelsConfig{
					Profiles: map[string]ModelProfile{
						"default": {DefaultModel: "default", defaultModelSet: true},
					},
					Definitions: map[string]ModelConfig{
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
				},
				Providers: map[string]ProviderConfig{
					"local": {
						Type:    ProviderTypeOpenAICompat,
						BaseURL: "http://localhost:11434/v1",
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
				SubAgent: SubAgentConfig{Enabled: false},
				Tools:    map[string]ToolConfig{},
				ProjectContext: ProjectContextConfig{
					MaxBytes: 8000,
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
				Modes: ModesConfig{
					Default: ExecutionModeBuild,
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

func TestMCPConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPConfig
		wantErr string
	}{
		{
			name: "empty config is valid",
			cfg:  MCPConfig{},
		},
		{
			name: "valid stdio server",
			cfg: MCPConfig{
				Enabled: true,
				Servers: map[string]MCPServerConfig{
					"example": {Enabled: true, Transport: "stdio", Command: "npx"},
				},
			},
		},
		{
			name: "valid http server",
			cfg: MCPConfig{
				Enabled: true,
				Servers: map[string]MCPServerConfig{
					"example": {Enabled: true, Transport: "http", URL: "http://localhost:3000"},
				},
			},
		},
		{
			name: "unsupported transport rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "grpc"},
				},
			},
			wantErr: `mcp.servers.example.transport: "grpc" is not supported (must be stdio or http)`,
		},
		{
			name: "stdio transport without command rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio"},
				},
			},
			wantErr: "mcp.servers.example.command is required",
		},
		{
			name: "http transport without url rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http"},
				},
			},
			wantErr: "mcp.servers.example.url is required",
		},
		{
			name: "stdio transport with url rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", URL: "http://localhost:3000"},
				},
			},
			wantErr: "mcp.servers.example.url: must be empty for stdio transport",
		},
		{
			name: "stdio transport with headers rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", Headers: map[string]string{"X-Custom": "value"}},
				},
			},
			wantErr: "mcp.servers.example.headers: must be empty for stdio transport",
		},
		{
			name: "http transport with command rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Command: "npx"},
				},
			},
			wantErr: "mcp.servers.example.command: must be empty for http transport",
		},
		{
			name: "http transport with args rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Args: []string{"arg1"}},
				},
			},
			wantErr: "mcp.servers.example.args: must be empty for http transport",
		},
		{
			name: "http transport with env rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Env: map[string]string{"VAR": "value"}},
				},
			},
			wantErr: "mcp.servers.example.env: must be empty for http transport",
		},
		{
			name: "http url with invalid scheme rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "ftp://localhost:3000"},
				},
			},
			wantErr: `mcp.servers.example.url: unsupported scheme "ftp" (must be http or https)`,
		},
		{
			name: "http url with no host rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://"},
				},
			},
			wantErr: "mcp.servers.example.url: must have a non-empty host",
		},
		{
			name: "http url with malformed url rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "ht!!tp://localhost"},
				},
			},
			wantErr: "mcp.servers.example.url:",
		},
		{
			name: "header with content-type (lowercase) rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"content-type": "application/json"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header "content-type" is reserved by the SDK`,
		},
		{
			name: "header with Content-Type (mixed case) rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"Content-Type": "application/json"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header "Content-Type" is reserved by the SDK`,
		},
		{
			name: "header with Mcp-Session-Id (lowercase) rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"mcp-session-id": "123"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header "mcp-session-id" is reserved by the SDK`,
		},
		{
			name: "header with Mcp-Param- prefix rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"Mcp-Param-Custom": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header "Mcp-Param-Custom" uses reserved Mcp-Param- prefix`,
		},
		{
			name: "header with Authorization is allowed",
			cfg: MCPConfig{
				Enabled: true,
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"Authorization": "Bearer token123"}},
				},
			},
		},
		{
			name: "header with empty name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header name must not be empty`,
		},
		{
			name: "header with space in name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"X Custom": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header name "X Custom" contains invalid characters`,
		},
		{
			name: "header with colon in name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"X:Custom": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header name "X:Custom" contains invalid characters`,
		},
		{
			name: "header with newline in name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"X\nCustom": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header name "X\nCustom" contains invalid characters`,
		},
		{
			name: "header with non-ASCII rune in name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"Ł": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header name "Ł" contains invalid characters`,
		},
		{
			name: "header with emoji in name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"X-Ok✓": "value"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header name "X-Ok✓" contains invalid characters`,
		},
		{
			name: "header with newline in value rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"X-Custom": "value\ninjection"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header "X-Custom" has a value containing a newline or carriage return`,
		},
		{
			name: "header with carriage return in value rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "http", URL: "http://localhost:3000", Headers: map[string]string{"X-Custom": "value\rinjection"}},
				},
			},
			wantErr: `mcp.servers.example.headers: header "X-Custom" has a value containing a newline or carriage return`,
		},
		{
			name: "server name with __ delimiter rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"a__b": {Transport: "stdio", Command: "npx"},
				},
			},
			wantErr: `server name must not contain "__"`,
		},
		{
			name: "empty server name rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"": {Transport: "stdio", Command: "npx"},
				},
			},
			wantErr: "contains an empty server name",
		},
		{
			name: "invalid approval value rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Approval: "always", Command: "npx"},
				},
			},
			wantErr: `mcp.servers.example.approval: "always" is invalid (must be ask, allow, or deny)`,
		},
		{
			name: "approval value is case-sensitive",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Approval: "Ask", Command: "npx"},
				},
			},
			wantErr: `mcp.servers.example.approval: "Ask" is invalid (must be ask, allow, or deny)`,
		},
		{
			name: "valid approval values accepted",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"ask":   {Approval: "ask", Command: "npx"},
					"allow": {Approval: "allow", Command: "npx"},
					"deny":  {Approval: "deny", Command: "npx"},
				},
			},
		},
		{
			name: "valid sub_agents accepted",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", SubAgents: []string{"explore", "code"}},
				},
			},
		},
		{
			name: "unknown sub_agents rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", SubAgents: []string{"bogus"}},
				},
			},
			wantErr: `mcp.servers.example.sub_agents: unknown agent type "bogus"`,
		},
		{
			name: "unknown allowed_tools and blocked_tools names accepted",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", AllowedTools: []string{"echo", "no_such_tool"}, BlockedTools: []string{"no_such_tool"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			cfg.MCP = tt.cfg
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

func TestSandboxConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SandboxConfig
		wantErr string
	}{
		{
			name: "empty config is valid",
			cfg:  SandboxConfig{},
		},
		{
			name: "exact name is valid",
			cfg:  SandboxConfig{EnvPassthrough: []string{"MYAPP_TOKEN"}},
		},
		{
			name: "trailing glob is valid",
			cfg:  SandboxConfig{EnvPassthrough: []string{"MYAPP_*"}},
		},
		{
			name:    "entry containing '=' rejected",
			cfg:     SandboxConfig{EnvPassthrough: []string{"MYAPP_TOKEN=value"}},
			wantErr: `sandbox.env_passthrough[0] ("MYAPP_TOKEN=value"): must not contain '='`,
		},
		{
			name:    "empty entry rejected",
			cfg:     SandboxConfig{EnvPassthrough: []string{"   "}},
			wantErr: `sandbox.env_passthrough[0] ("   "): must not be empty`,
		},
		{
			name:    "star not in final position rejected",
			cfg:     SandboxConfig{EnvPassthrough: []string{"FOO*BAR"}},
			wantErr: `sandbox.env_passthrough[0] ("FOO*BAR"): '*' is only permitted as the final character`,
		},
		{
			name:    "leading star rejected",
			cfg:     SandboxConfig{EnvPassthrough: []string{"*FOO"}},
			wantErr: `sandbox.env_passthrough[0] ("*FOO"): '*' is only permitted as the final character`,
		},
		{
			name:    "bare star rejected",
			cfg:     SandboxConfig{EnvPassthrough: []string{"*"}},
			wantErr: `sandbox.env_passthrough[0] ("*"): a bare '*' would pass through the entire environment; use sandbox.env_passthrough_all instead`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			cfg.Sandbox = tt.cfg
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

func TestDesktopNotificationsValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     desktopNotificationsConfig
		wantErr string
	}{
		{
			name: "valid config with enabled and duration",
			cfg: desktopNotificationsConfig{
				Enabled:  true,
				Duration: 10,
			},
			wantErr: "",
		},
		{
			name: "valid config with zero duration (permanent)",
			cfg: desktopNotificationsConfig{
				Enabled:  true,
				Duration: 0,
			},
			wantErr: "",
		},
		{
			name: "valid config disabled",
			cfg: desktopNotificationsConfig{
				Enabled:  false,
				Duration: 0,
			},
			wantErr: "",
		},
		{
			name: "negative duration rejected",
			cfg: desktopNotificationsConfig{
				Enabled:  true,
				Duration: -1,
			},
			wantErr: "desktop_notifications.duration must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			cfg.DesktopNotifications = tt.cfg
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

func TestValidateCodexTransport(t *testing.T) {
	tests := []struct {
		name        string
		transport   CodexTransport
		wantErr     bool
		wantErrText string
	}{
		{name: "http is valid", transport: CodexTransportHTTP, wantErr: false},
		{name: "websocket is valid", transport: CodexTransportWebSocket, wantErr: false},
		{name: "empty string defaults to http", transport: "", wantErr: false},
		{name: "invalid transport is rejected", transport: "invalid", wantErr: true, wantErrText: "codex.transport"},
		{name: "auto is rejected with a removal-specific message", transport: "auto", wantErr: true, wantErrText: "was removed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			cfg.Providers["codex"] = ProviderConfig{
				Type: ProviderTypeCodex,
				Codex: CodexConfig{
					Transport: tt.transport,
				},
			}
			err := validate(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("validate() error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("validate() error = %q, want substring containing %q", err.Error(), tt.wantErrText)
				}
			} else if err != nil {
				t.Fatalf("validate() error = %v, want nil", err)
			}
		})
	}
}
