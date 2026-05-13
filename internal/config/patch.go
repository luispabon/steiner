package config

// configPatch represents a partial config update from YAML.
type configPatch struct {
	Scheduler         *schedulerPatch         `yaml:"scheduler"`
	Model             *modelPatch             `yaml:"model"`
	ModelAlias        string                  `yaml:"-"` // populated when model is a scalar alias string
	Models            *map[string]modelPatch  `yaml:"models"`
	Limits            *limitsPatch            `yaml:"limits"`
	Approval          *approvalPatch          `yaml:"approval"`
	SubAgent          *subAgentPatch          `yaml:"sub_agent"`
	Tools             *map[string]toolPatch   `yaml:"tools"`
	ProjectContext    *projectContextPatch    `yaml:"project_context"`
	Paths             *pathsPatch             `yaml:"paths"`
	Logging           *loggingPatch           `yaml:"logging"`
	Debug             *debugPatch             `yaml:"debug"`
	ContextManagement *contextManagementPatch `yaml:"context_management"`
}

type contextManagementPatch struct {
	Mode               *ContextMode        `yaml:"mode"`
	CompactionStrategy *CompactionStrategy `yaml:"compaction_strategy"`
	MaskingWindowTurns *int                `yaml:"masking_window_turns"`
	ReadAnnotations    *bool               `yaml:"read_annotations"`
	ScratchpadMode     *ScratchpadMode     `yaml:"scratchpad_mode"`
}

type schedulerPatch struct {
	Parallelism *int `yaml:"parallelism"`
}

type thinkingConfigPatch struct {
	Enabled                  *bool           `yaml:"enabled"`
	EnabledScaffoldInference *bool           `yaml:"enabled_scaffolding_inference"`
	DisableMarker            *string         `yaml:"disable_marker"`
	Params                   *map[string]any `yaml:"params"`
}

type modelPatch struct {
	Type                *string              `yaml:"type"`
	BaseURL             *string              `yaml:"base_url"`
	APIKey              *string              `yaml:"api_key"`
	Model               *string              `yaml:"model"`
	ExtraParams         *map[string]any      `yaml:"extra_params"`
	MaxCompletionTokens *int                 `yaml:"max_completion_tokens"`
	ContextSize         *int                 `yaml:"context_size"`
	Retry               *retryPatch          `yaml:"retry"`
	Compaction          *compactionPatch     `yaml:"compaction"`
	Prompts             *modelPromptsPatch   `yaml:"prompts"`
	Thinking            *thinkingConfigPatch `yaml:"thinking"`
}

type retryPatch struct {
	Enabled        *bool     `yaml:"enabled"`
	MaxAttempts    *int      `yaml:"max_attempts"`
	InitialBackoff *Duration `yaml:"initial_backoff"`
	MaxBackoff     *Duration `yaml:"max_backoff"`
	RetryAfterMax  *Duration `yaml:"retry_after_max"`
}

type modelPromptsPatch struct {
	System     *string `yaml:"system"`
	Compaction *string `yaml:"compaction"`
}

type compactionPatch struct {
	SafetyMarginTokens *int `yaml:"safety_margin_tokens"`
	SummaryMaxTokens   *int `yaml:"summary_max_tokens"`
}

type limitsPatch struct {
	MaxTurns           *int                 `yaml:"max_turns"`
	MaxTokens          *int                 `yaml:"max_tokens"`
	ToolTimeoutDefault *Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       *map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes *int                 `yaml:"tool_output_max_bytes"`
}

type approvalPatch struct {
	Default       *ApprovalMode             `yaml:"default"`
	ToolOverrides *map[string]*ApprovalMode `yaml:"tool_overrides"`
}

type subAgentPatch struct {
	Enabled      *bool     `yaml:"enabled"`
	MaxTurns     *int      `yaml:"max_turns"`
	MaxTokens    *int      `yaml:"max_tokens"`
	AllowedTools *[]string `yaml:"allowed_tools"`
}

type toolPatch struct {
	Exec        *string         `yaml:"exec"`
	Subcommand  *string         `yaml:"subcommand"`
	Description *string         `yaml:"description"`
	Parameters  *map[string]any `yaml:"parameters"`
	Timeout     *Duration       `yaml:"timeout"`
	Approval    *ApprovalMode   `yaml:"approval"`
	Constraints *map[string]any `yaml:"constraints"`
}

type projectContextPatch struct {
	MaxTokens   *int      `yaml:"max_tokens"`
	ExtraFiles  *[]string `yaml:"extra_files"`
	IgnoreFiles *[]string `yaml:"ignore_files"`
}

type pathsPatch struct {
	ProjectRootOnly *bool     `yaml:"project_root_only"`
	WritablePaths   *[]string `yaml:"writable_paths"`
	BlockedPaths    *[]string `yaml:"blocked_paths"`
	ExcludePaths    *[]string `yaml:"exclude_paths"`
	ExcludePatterns *[]string `yaml:"exclude_patterns"`
}

type loggingPatch struct {
	Enabled       *bool   `yaml:"enabled"`
	Level         *string `yaml:"level"`
	File          *string `yaml:"file"`
	ThinkingChunk *bool   `yaml:"thinking_chunk"`
}

type debugPatch struct {
	ShowInternalScaffoldInference *bool `yaml:"show_internal_scaffold_inference"`
}
