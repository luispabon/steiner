package config

func defaultConfig() Config {
	return Config{
		Scheduler: SchedulerConfig{
			Parallelism: 1,
		},
		Model: "default",
		Models: map[string]ModelConfig{
			"default": {
				Type:                "openai_compat",
				BaseURL:             "http://localhost:11434/v1",
				APIKey:              "",
				MaxCompletionTokens: 8192,
				ContextSize:         32768,
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
				"bash":  MustDuration("120s"),
				"read":  MustDuration("5s"),
				"write": MustDuration("5s"),
			},
			ToolOutputMaxBytes: 65536,
		},
		Approval: ApprovalConfig{
			Default: ApprovalModePrompt,
			Overrides: map[string]ApprovalMode{
				"read":   ApprovalModeAuto,
				"glob":   ApprovalModeAuto,
				"search": ApprovalModeAuto,
				"write":  ApprovalModePrompt,
				"bash":   ApprovalModePrompt,
			},
		},
		SubAgent: SubAgentConfig{
			Enabled:       false,
			MaxTurns:      15,
			MaxTokens:     100000,
			AllowedTools:  []string{"read", "glob", "search", "write", "bash"},
			AllowNesting:  false,
			MaxConcurrent: 1,
		},
		Tools: make(map[string]ToolConfig),
		ProjectContext: ProjectContextConfig{
			MaxTokens:   2000,
			ExtraFiles:  []string{},
			IgnoreFiles: []string{},
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
