package config

func defaultConfig() Config {
	defaultModel := ModelConfig{
		Type:                "openai_compat",
		BaseURL:             "http://localhost:11434/v1",
		APIKey:              "",
		Model:               "qwen3-35b-a3b",
		MaxCompletionTokens: 8192,
		ContextSize:         32768,
		Compaction: CompactionConfig{
			SafetyMarginTokens: 8192,
			SummaryMaxTokens:   4096,
		},
	}
	return Config{
		Scheduler: SchedulerConfig{
			Parallelism: 1,
		},
		Model: defaultModel,
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
			Enabled:       false,
			MaxTurns:      15,
			MaxTokens:     100000,
			AllowedTools:  []string{"read", "glob", "grep", "ls", "write", "edit", "bash"},
			AllowNesting:  false,
			MaxConcurrent: 1,
		},
		Tools: make(map[string]ToolConfig),
		ProjectContext: ProjectContextConfig{
			MaxTokens:   2000,
			ExtraFiles:  nil,
			IgnoreFiles: nil,
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
	}
}
