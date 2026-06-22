package config

// configPatch represents a partial config update from YAML.
type configPatch struct {
	CaveHuman            *bool                      `yaml:"cave_human"`
	Scheduler            *schedulerPatch            `yaml:"scheduler"`
	DefaultModel         *string                    `yaml:"default_model"`
	Providers            *map[string]providerPatch  `yaml:"providers"`
	Models               *map[string]modelPatch     `yaml:"models"`
	Limits               *limitsPatch               `yaml:"limits"`
	SubAgent             *subAgentPatch             `yaml:"sub_agent"`
	Advisor              *advisorPatch              `yaml:"advisor"`
	OneShot              *oneshotPatch              `yaml:"oneshot"`
	WorkflowHandoff      *workflowHandoffPatch      `yaml:"workflow_handoff"`
	DesktopNotifications *desktopNotificationsPatch `yaml:"desktop_notifications"`
	Tools                *map[string]toolPatch      `yaml:"tools"`
	ProjectContext       *projectContextPatch       `yaml:"project_context"`
	Paths                *pathsPatch                `yaml:"paths"`
	Logging              *loggingPatch              `yaml:"logging"`
	ContextManagement    *contextManagementPatch    `yaml:"context_management"`
	Search               *searchPatch               `yaml:"search"`
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
	ReadAnnotations *bool `yaml:"read_annotations"`
}

type schedulerPatch struct {
	Parallelism *int `yaml:"parallelism"`
}

type modelPatch struct {
	Provider     *string            `yaml:"provider"`
	ID           *string            `yaml:"id"`
	Params       *map[string]any    `yaml:"params"`
	ExtraParams  *map[string]any    `yaml:"extra_params"`
	PromptSuffix *string            `yaml:"prompt_suffix"`
	Retry        *retryPatch        `yaml:"retry"`
	Prompts      *modelPromptsPatch `yaml:"prompts"`
	Advanced     *advancedPatch     `yaml:"advanced"`
	Vision       *bool              `yaml:"vision"`
}

type advancedPatch struct {
	Limits    *advancedLimitsPatch `yaml:"limits"`
	Transport *ModelTransportType  `yaml:"transport"`
}

type advancedLimitsPatch struct {
	ContextWindow   *int `yaml:"context_window"`
	MaxOutputTokens *int `yaml:"max_output_tokens"`
}

type retryPatch struct {
	Enabled        *bool     `yaml:"enabled"`
	MaxAttempts    *int      `yaml:"max_attempts"`
	InitialBackoff *Duration `yaml:"initial_backoff"`
	MaxBackoff     *Duration `yaml:"max_backoff"`
	RetryAfterMax  *Duration `yaml:"retry_after_max"`
}

type modelPromptsPatch struct {
	System       *string `yaml:"system"`
	Compaction   *string `yaml:"compaction"`
	SystemSuffix *string `yaml:"system_suffix,omitempty"`
}

type limitsPatch struct {
	MaxTurns           *int                 `yaml:"max_turns"`
	MaxTokens          *int                 `yaml:"max_tokens"`
	ToolTimeoutDefault *Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       *map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes *int                 `yaml:"tool_output_max_bytes"`
}

type agentConfigPatch struct {
	Model *string `yaml:"model"`
}

type subAgentPatch struct {
	Enabled      *bool                        `yaml:"enabled"`
	MaxTurns     *int                         `yaml:"max_turns"`
	MaxTokens    *int                         `yaml:"max_tokens"`
	AllowedTools *[]string                    `yaml:"allowed_tools"`
	Agents       *map[string]agentConfigPatch `yaml:"agents"`
}

type advisorPatch struct {
	Enabled       *bool   `yaml:"enabled"`
	Model         *string `yaml:"model"`
	MaxUsesPerRun *int    `yaml:"max_uses_per_run"`
	MaxTokens     *int    `yaml:"max_tokens"`
}

type oneshotPatch struct {
	Models *map[string]string `yaml:"models"`
	AutoPR *bool              `yaml:"auto_pr"`
}

type workflowHandoffPatch struct {
	Models *map[string]string `yaml:"models"`
}

type desktopNotificationsPatch struct {
	Enabled  *bool `yaml:"enabled"`
	Duration *int  `yaml:"duration"`
}

type toolPatch struct {
	Exec        *string         `yaml:"exec"`
	Subcommand  *string         `yaml:"subcommand"`
	Description *string         `yaml:"description"`
	Parameters  *map[string]any `yaml:"parameters"`
	Timeout     *Duration       `yaml:"timeout"`
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

type searchPatch struct {
	Backend      *string `yaml:"backend"`
	SearxngURL   *string `yaml:"searxng_url"`
	GoogleCx     *string `yaml:"google_cx"`
	GoogleAPIKey *string `yaml:"google_api_key"`
	KagiAPIKey   *string `yaml:"kagi_api_key"`
	BraveAPIKey  *string `yaml:"brave_api_key"`
}
