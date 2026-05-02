package config

import (
	"fmt"
	"strings"
)

// ContextMode controls which context management strategy the agent uses.
type ContextMode string

const (
	// ContextModeNaive is the default pass-through mode that keeps existing
	// compaction behaviour unchanged.
	ContextModeNaive ContextMode = "naive"
	// ContextModeSmart enables active context management with signal extraction
	// and structured state.
	ContextModeSmart ContextMode = "smart"
)

// CompactionStrategy controls how the smart context manager reduces context
// when the conversation grows too large.
type CompactionStrategy string

const (
	// CompactionStrategyDrop discards old turns without summarising.
	CompactionStrategyDrop CompactionStrategy = "drop"
	// CompactionStrategySummarize summarises dropped turns before removing them.
	CompactionStrategySummarize CompactionStrategy = "summarize"
	// CompactionStrategyHybrid combines summarisation with selective retention.
	CompactionStrategyHybrid CompactionStrategy = "hybrid"
)

// ContextManagementConfig holds settings for the active context management
// feature.
type ContextManagementConfig struct {
	Mode               ContextMode        `yaml:"mode"`
	CompactionStrategy CompactionStrategy `yaml:"compaction_strategy"`
	MaskingWindowTurns int                `yaml:"masking_window_turns"`
	ReadAnnotations    bool               `yaml:"read_annotations"`
}

// Config is the complete application configuration.
type Config struct {
	Scheduler         SchedulerConfig         `yaml:"scheduler"`
	Model             ModelConfig             `yaml:"model"`
	Models            map[string]ModelConfig  `yaml:"models"`
	Limits            LimitsConfig            `yaml:"limits"`
	Approval          ApprovalConfig          `yaml:"approval"`
	SubAgent          SubAgentConfig          `yaml:"sub_agent"`
	Tools             map[string]ToolConfig   `yaml:"tools"`
	ProjectContext    ProjectContextConfig    `yaml:"project_context"`
	Paths             PathsConfig             `yaml:"paths"`
	Logging           LoggingConfig           `yaml:"logging"`
	ContextManagement ContextManagementConfig `yaml:"context_management"`
}

type SchedulerConfig struct {
	Parallelism int `yaml:"parallelism"`
}

// ModelConfig configures a model provider instance.
type ModelConfig struct {
	Type                string           `yaml:"type"`
	BaseURL             string           `yaml:"base_url"`
	APIKey              string           `yaml:"api_key"`
	Model               string           `yaml:"model"`
	ExtraParams         map[string]any   `yaml:"extra_params"`
	MaxCompletionTokens int              `yaml:"max_completion_tokens"`
	ContextSize         int              `yaml:"context_size"`
	Compaction          CompactionConfig `yaml:"compaction"`
	Prompts             ModelPrompts     `yaml:"prompts"`
}

// ModelPrompts contains per-model prompt overrides. These override the
// embedded default prompts and are only settable via config file.
type ModelPrompts struct {
	System     string `yaml:"system"`
	Compaction string `yaml:"compaction"`
}

type CompactionConfig struct {
	SafetyMarginTokens int `yaml:"safety_margin_tokens"`
	SummaryMaxTokens   int `yaml:"summary_max_tokens"`
}

type LimitsConfig struct {
	MaxTurns           int                 `yaml:"max_turns"`
	MaxTokens          int                 `yaml:"max_tokens"`
	ToolTimeoutDefault Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes int                 `yaml:"tool_output_max_bytes"`
}

type ApprovalMode string

const (
	ApprovalModeAuto   ApprovalMode = "auto"
	ApprovalModePrompt ApprovalMode = "prompt"
	ApprovalModeDeny   ApprovalMode = "deny"
)

type ApprovalConfig struct {
	Default       ApprovalMode             `yaml:"default"`
	ToolOverrides map[string]*ApprovalMode `yaml:"tool_overrides"`
}

type SubAgentConfig struct {
	Enabled       bool     `yaml:"enabled"`
	MaxTurns      int      `yaml:"max_turns"`
	MaxTokens     int      `yaml:"max_tokens"`
	AllowedTools  []string `yaml:"allowed_tools"`
	AllowNesting  bool     `yaml:"allow_nesting"`
	MaxConcurrent int      `yaml:"max_concurrent"`
}

type ToolConfig struct {
	Exec        string         `yaml:"exec"`
	Subcommand  string         `yaml:"subcommand"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	Timeout     Duration       `yaml:"timeout"`
	Approval    ApprovalMode   `yaml:"approval"`
	Constraints map[string]any `yaml:"constraints"`
}

type ProjectContextConfig struct {
	MaxTokens   int      `yaml:"max_tokens"`
	ExtraFiles  []string `yaml:"extra_files"`
	IgnoreFiles []string `yaml:"ignore_files"`
}

type PathsConfig struct {
	ProjectRootOnly bool     `yaml:"project_root_only"`
	WritablePaths   []string `yaml:"writable_paths"`
	BlockedPaths    []string `yaml:"blocked_paths"`
	ExcludePaths    []string `yaml:"exclude_paths"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
}

type LoggingConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Level         string `yaml:"level"`
	File          string `yaml:"file"`
	ThinkingChunk bool   `yaml:"thinking_chunk"`
}

// SwitchModelConfigByAlias looks up a model config by alias and updates cfg.Model
// to point to it. It returns the selected ModelConfig.
func SwitchModelConfigByAlias(cfg *Config, alias string) (ModelConfig, error) {
	if cfg == nil {
		return ModelConfig{}, fmt.Errorf("config is required")
	}
	model, err := SelectedModelConfigByAlias(*cfg, alias)
	if err != nil {
		return ModelConfig{}, err
	}
	cfg.Model = model
	return model, nil
}

// SelectedModelConfigByAlias looks up a model config by its alias in the
// Models map. It returns an error if the alias is empty or not found.
func SelectedModelConfigByAlias(cfg Config, alias string) (ModelConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return ModelConfig{}, fmt.Errorf("model is required")
	}
	model, ok := cfg.Models[alias]
	if !ok {
		return ModelConfig{}, fmt.Errorf("model %q is not defined", alias)
	}
	return model, nil
}

// copyStringAnyMap creates a shallow copy of a map[string]any.
func copyStringAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
