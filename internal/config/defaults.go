package config

var (
	advisorTimeout = MustDuration("180s")

	// DefaultCodexMinRequestInterval is the minimum gap enforced between
	// consecutive Codex requests when a provider omits
	// codex.min_request_interval. Exported so internal/provider can seed it
	// for user-declared providers with type: codex, which do not go through
	// defaultConfig.
	DefaultCodexMinRequestInterval = MustDuration("4s")
)

func defaultConfig() Config {
	defaultProvider := ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:11434/v1",
		Timeout: MustDuration("30s"),
		Codex: CodexConfig{
			MinRequestInterval: DefaultCodexMinRequestInterval,
		},
	}
	defaultModel := ModelConfig{
		Provider: "local",
		ID:       "qwen3-35b-a3b",
		Retry: RetryConfig{
			Enabled:        true,
			MaxAttempts:    5,
			InitialBackoff: MustDuration("250ms"),
			MaxBackoff:     MustDuration("5s"),
			RetryAfterMax:  MustDuration("60s"),
		},
		Advanced: AdvancedConfig{
			Limits: AdvancedLimitsConfig{
				ContextWindow:   32768,
				MaxOutputTokens: 8192,
			},
		},
	}
	return Config{
		Scheduler: SchedulerConfig{
			Parallelism: 1,
		},
		Providers: map[string]ProviderConfig{
			"local": defaultProvider,
		},
		Models: ModelsConfig{
			Default: "default",
			Definitions: map[string]ModelConfig{
				"default": defaultModel,
			},
		},
		Limits: LimitsConfig{
			MaxTurns:           50,
			MaxTokens:          500000,
			ToolTimeoutDefault: MustDuration("30s"),
			ToolTimeouts: map[string]Duration{
				"bash": MustDuration("120s"),
				"read": MustDuration("5s"),
				"grep": MustDuration("30s"),
				"ls":   MustDuration("5s"),
			},
			ToolOutputMaxBytes: 65536,
		},
		Sandbox: SandboxConfig{
			Enabled: true,
		},
		SubAgent: SubAgentConfig{
			Enabled:      true,
			MaxTurns:     30,
			MaxTokens:    100000,
			AllowedTools: []string{"read", "glob", "grep", "ls", "bash"},
		},
		Advisor: AdvisorConfig{
			Enabled:       false,
			MaxUsesPerRun: 3,
			Timeout:       &advisorTimeout,
		},
		OneShot: oneshotConfig{
			AutoPR: false,
		},
		Tools: make(map[string]ToolConfig),
		ProjectContext: ProjectContextConfig{
			MaxTokens: 2000,
		},
		Paths: PathsConfig{
			ProjectRootOnly: true,
			WritablePaths:   []string{},
			BlockedPaths:    []string{},
		},
		Logging: LoggingConfig{
			Level: "info",
			File:  "~/.local/share/steiner/steiner.log",
		},
		ContextManagement: ContextManagementConfig{
			ReadAnnotations: true,
		},
		CaveHuman: false,
	}
}
