package config

// configPatch represents a partial config update from YAML.
type configPatch struct {
	Scheduler         *schedulerPatch           `yaml:"scheduler"`
	DefaultModel      *string                   `yaml:"default_model"`
	Providers         *map[string]providerPatch `yaml:"providers"`
	Models            *map[string]modelPatch    `yaml:"models"`
	Limits            *limitsPatch              `yaml:"limits"`
	Approval          *approvalPatch            `yaml:"approval"`
	SubAgent          *subAgentPatch            `yaml:"sub_agent"`
	Tools             *map[string]toolPatch     `yaml:"tools"`
	ProjectContext    *projectContextPatch      `yaml:"project_context"`
	Paths             *pathsPatch               `yaml:"paths"`
	Logging           *loggingPatch             `yaml:"logging"`
	Debug             *debugPatch               `yaml:"debug"`
	ContextManagement *contextManagementPatch   `yaml:"context_management"`
}

type providerPatch struct {
	Type      *ProviderType      `yaml:"type"`
	BaseURL   *string            `yaml:"base_url"`
	APIKey    *string            `yaml:"api_key"`
	APIKeyEnv *string            `yaml:"api_key_env"`
	Headers   *map[string]string `yaml:"headers"`
	Timeout   *Duration          `yaml:"timeout"`
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

type modelPatch struct {
	Provider                  *string            `yaml:"provider"`
	ID                        *string            `yaml:"id"`
	Params                    *map[string]any    `yaml:"params"`
	ExtraParams               *map[string]any    `yaml:"extra_params"`
	ThinkingEnabled           *bool              `yaml:"thinking_enabled"`
	ThinkingDisableMarker     *string            `yaml:"thinking_disable_marker"`
	ThinkingScaffoldInference *bool              `yaml:"thinking_scaffold_inference"`
	ThinkingParams            *map[string]any    `yaml:"thinking_params"`
	Retry                     *retryPatch        `yaml:"retry"`
	Prompts                   *modelPromptsPatch `yaml:"prompts"`
	Advanced                  *advancedPatch     `yaml:"advanced"`
}

type advancedPatch struct {
	Limits *advancedLimitsPatch `yaml:"limits"`
}

type advancedLimitsPatch struct {
	ContextWindow       *int `yaml:"context_window"`
	MaxOutputTokens     *int `yaml:"max_output_tokens"`
	OutputReserveTokens *int `yaml:"output_reserve_tokens"`
	SafetyMarginTokens  *int `yaml:"safety_margin_tokens"`
	SummaryMaxTokens    *int `yaml:"summary_max_tokens"`
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
