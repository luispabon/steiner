package config

// Config is the complete application configuration.
type Config struct {
	Scheduler      SchedulerConfig        `yaml:"scheduler"`
	Model          string                 `yaml:"model"`
	Models         map[string]ModelConfig `yaml:"models"`
	Limits         LimitsConfig           `yaml:"limits"`
	Approval       ApprovalConfig         `yaml:"approval"`
	SubAgent       SubAgentConfig         `yaml:"sub_agent"`
	Tools          map[string]ToolConfig  `yaml:"tools"`
	ProjectContext ProjectContextConfig   `yaml:"project_context"`
	Paths          PathsConfig            `yaml:"paths"`
	Logging        LoggingConfig          `yaml:"logging"`
}

type SchedulerConfig struct {
	Parallelism int `yaml:"parallelism"`
}

type ModelConfig struct {
	Type                string           `yaml:"type"`
	BaseURL             string           `yaml:"base_url"`
	APIKey              string           `yaml:"api_key"`
	Model               string           `yaml:"model"`
	Temperature         *float64         `yaml:"temperature"`
	MaxCompletionTokens int              `yaml:"max_completion_tokens"`
	ContextSize         int              `yaml:"context_size"`
	Compaction          CompactionConfig `yaml:"compaction"`
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
	Default   ApprovalMode            `yaml:"default"`
	Overrides map[string]ApprovalMode `yaml:"overrides"`
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
}

type LoggingConfig struct {
	Enabled bool   `yaml:"enabled"`
	Level   string `yaml:"level"`
	File    string `yaml:"file"`
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
