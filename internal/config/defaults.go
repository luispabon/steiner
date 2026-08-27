package config

var (
	// DefaultCodexMinRequestInterval is the minimum gap enforced between
	// consecutive Codex requests when a provider omits
	// codex.min_request_interval. Exported so internal/provider can seed it
	// for user-declared providers with type: codex, which do not go through
	// defaultConfig. Defaults to 0 (pacing disabled); the knob remains for
	// users who want to re-enable client-side request pacing.
	DefaultCodexMinRequestInterval = MustDuration("0s")
)

func newModelConfigBase() ModelConfig {
	return ModelConfig{
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
}

// NewModelConfigBase returns default values for a model definition.
func NewModelConfigBase() ModelConfig {
	return newModelConfigBase()
}

// advisorTimeout returns a fresh Duration for the default Advisor.Timeout so
// each Config produced by defaultConfig owns its own pointer.
func advisorTimeout() *Duration {
	timeout := MustDuration("180s")
	return &timeout
}

func defaultConfig() Config {
	defaultProvider := ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:11434/v1",
		Timeout: MustDuration("30s"),
		Codex: CodexConfig{
			MinRequestInterval: DefaultCodexMinRequestInterval,
			Transport:          CodexTransportHTTP,
		},
	}
	defaultModel := newModelConfigBase()
	defaultModel.Provider = "local"
	defaultModel.ID = "qwen3-35b-a3b"
	return Config{
		TUI: TUIConfig{
			FPS: 60,
		},
		Providers: map[string]ProviderConfig{
			"local": defaultProvider,
		},
		Models: ModelsConfig{
			DiscoveryEnabled: true,
			Profiles: map[string]ModelProfile{
				"default": {DefaultModel: "default", defaultModelSet: true},
			},
			Definitions: map[string]ModelConfig{
				"default": defaultModel,
			},
		},
		Limits: LimitsConfig{
			MaxTurns:           50,
			MaxTokens:          500000,
			ModelCallTimeout:   MustDuration("10m"),
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
			Enabled:                      true,
			WarningOnUnsupportedPlatform: true,
		},
		SubAgent: SubAgentConfig{
			Enabled:     true,
			MaxTurns:    30,
			MaxTokens:   100000,
			MaxParallel: 3,
		},
		Advisor: AdvisorConfig{
			Enabled:       false,
			MaxUsesPerRun: 3,
			Timeout:       advisorTimeout(),
		},
		OneShot: oneshotConfig{
			AutoPR: false,
		},
		Tools: make(map[string]ToolConfig),
		ProjectContext: ProjectContextConfig{
			MaxBytes: 8000,
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
		MCP: MCPConfig{
			Enabled: true,
		},
		Modes: ModesConfig{
			Default: ExecutionModeBuild,
		},
	}
}
