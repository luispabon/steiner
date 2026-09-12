package config

import (
	"os"
	"path/filepath"
)

// defaultDiagnosticsDir is the diagnostics directory used when
// diagnostics.dir is unset. Diagnostics are user state, not repo content and
// not derived from logging.file, so they live under XDG_STATE_HOME (the same
// base internal/usagestats uses for its store). The "~" form is expanded by
// normalizePaths once the home directory is resolved.
func defaultDiagnosticsDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "diagnostics")
	}
	return filepath.Join("~", ".local", "state", "steiner", "diagnostics")
}

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
				"default": {DefaultModel: "default"},
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
			MaxParallelTools:   4,
		},
		Sandbox: SandboxConfig{
			Enabled:                      true,
			WarningOnUnsupportedPlatform: true,
		},
		SubAgent: SubAgentConfig{
			Enabled:            true,
			MaxTurns:           30,
			MaxTokens:          100000,
			MaxParallel:        3,
			MaxFollowUps:       5,
			OrchestrationLevel: OrchestrationLevelStandard,
		},
		Advisor: AdvisorConfig{
			Enabled:            false,
			MaxUsesPerRun:      3,
			MaxUsesPerSubAgent: 1,
			Timeout:            advisorTimeout(),
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
		Diagnostics: DiagnosticsConfig{
			Enabled:       false,
			Dir:           defaultDiagnosticsDir(),
			RetentionDays: 30,
		},
		ContextManagement: ContextManagementConfig{
			ReadAnnotations: true,
		},
		CaveHuman: false,
		MCP: MCPConfig{
			Enabled: true,
		},
		// The four timeouts below are calibrated against gopls v0.23.0; see
		// docs/lsp.md "Timeout calibration" for the measurements and
		// internal/lsp/calibrate_manual_test.go for the harness that produced
		// them. IdleTimeout is a policy choice, not a measurement.
		LSP: LSPConfig{
			Enabled:           false,
			IdleTimeout:       MustDuration("5m"),
			RequestTimeout:    MustDuration("10s"),
			ReadyTimeout:      MustDuration("30s"),
			ReadyGracePeriod:  MustDuration("2s"),
			DiagnosticsWindow: MustDuration("2s"),
			MaxResults:        200,
		},
		Modes: ModesConfig{
			Default: ExecutionModeBuild,
		},
		UpdateCheck: UpdateCheckConfig{
			Enabled:       true,
			IntervalHours: 6,
		},
	}
}
