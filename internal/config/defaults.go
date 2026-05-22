package config

func defaultConfig() Config {
	defaultProvider := ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:11434/v1",
		Timeout: MustDuration("30s"),
	}
	defaultModel := ModelConfig{
		Provider: "local",
		ID:       "qwen3-35b-a3b",
		Retry: RetryConfig{
			Enabled:        true,
			MaxAttempts:    3,
			InitialBackoff: MustDuration("250ms"),
			MaxBackoff:     MustDuration("5s"),
			RetryAfterMax:  MustDuration("30s"),
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
		DefaultModel: "default",
		Providers: map[string]ProviderConfig{
			"local": defaultProvider,
		},
		Models: map[string]ModelConfig{
			"default": defaultModel,
		},
		Limits: LimitsConfig{
			MaxTurns:           50,
			MaxTokens:          500000,
			ToolTimeoutDefault: MustDuration("30s"),
			ToolTimeouts: map[string]Duration{
				"bash":  MustDuration("120s"),
				"read":  MustDuration("5s"),
				"write": MustDuration("5s"),
				"edit":  MustDuration("5s"),
				"grep":  MustDuration("30s"),
				"ls":    MustDuration("5s"),
			},
			ToolOutputMaxBytes: 65536,
		},
		Approval: ApprovalConfig{
			Default: ApprovalModeAuto,
		},
		SubAgent: SubAgentConfig{
			Enabled:      true,
			MaxTurns:     30,
			MaxTokens:    100000,
			AllowedTools: []string{"read", "glob", "grep", "ls", "write", "edit", "bash", "scratchpad"},
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
		Debug: DebugConfig{
			ShowInternalScaffoldInference: false,
		},
		ContextManagement: ContextManagementConfig{
			Mode:               ContextModeNaive,
			CompactionStrategy: CompactionStrategyDrop,
			MaskingWindowTurns: 5,
			ReadAnnotations:    true,
			ScratchpadMode:     ScratchpadModeScaffoldOnly,
		},
		CavemanMode: false,
	}
}
