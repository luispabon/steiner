package config

// configPatch represents a partial config update from YAML.
type configPatch struct {
	CaveHuman            *bool                      `yaml:"cave_human"`
	TUI                  *tuiPatch                  `yaml:"tui"`
	Providers            *map[string]providerPatch  `yaml:"providers"`
	Sandbox              *sandboxPatch              `yaml:"sandbox"`
	Permissions          *permissionsPatch          `yaml:"permissions"`
	Models               *modelsPatch               `yaml:"models"`
	Limits               *limitsPatch               `yaml:"limits"`
	SubAgent             *subAgentPatch             `yaml:"sub_agent"`
	Advisor              *advisorPatch              `yaml:"advisor"`
	OneShot              *oneshotPatch              `yaml:"oneshot"`
	DesktopNotifications *desktopNotificationsPatch `yaml:"desktop_notifications"`
	Tools                *map[string]toolPatch      `yaml:"tools"`
	ProjectContext       *projectContextPatch       `yaml:"project_context"`
	Paths                *pathsPatch                `yaml:"paths"`
	Logging              *loggingPatch              `yaml:"logging"`
	ContextManagement    *contextManagementPatch    `yaml:"context_management"`
	Search               *searchPatch               `yaml:"search"`
	MCP                  *mcpPatch                  `yaml:"mcp"`
	Modes                *modesPatch                `yaml:"modes"`
}

type modelsPatch struct {
	DiscoveryEnabled *bool                    `yaml:"discovery_enabled"`
	Definitions      *map[string]modelPatch   `yaml:"definitions"`
	Profiles         *map[string]profilePatch `yaml:"profiles"`
}

type profilePatch struct {
	DefaultModel    *string            `yaml:"default_model"`
	Advisor         *string            `yaml:"advisor"`
	SubAgents       *map[string]string `yaml:"sub_agents"`
	OneShot         *map[string]string `yaml:"oneshot"`
	WorkflowHandoff *map[string]string `yaml:"workflow_handoff"`
}

type providerPatch struct {
	Type      *ProviderType      `yaml:"type"`
	BaseURL   *string            `yaml:"base_url"`
	APIKey    *string            `yaml:"api_key"`
	APIKeyEnv *string            `yaml:"api_key_env"`
	Headers   *map[string]string `yaml:"headers"`
	Timeout   *Duration          `yaml:"timeout"`
	Codex     *codexPatch        `yaml:"codex"`
}

type codexPatch struct {
	MinRequestInterval *Duration       `yaml:"min_request_interval"`
	Transport          *CodexTransport `yaml:"transport"`
}

type contextManagementPatch struct {
	ReadAnnotations *bool `yaml:"read_annotations"`
}

type tuiPatch struct {
	FPS *int `yaml:"fps"`
}

// sandboxPatch holds sandbox config fields that can be patched from YAML.
type sandboxPatch struct {
	Enabled                      *bool        `yaml:"enabled"`
	WarningOnUnsupportedPlatform *bool        `yaml:"warning_on_unsupported_platform"`
	EnvPassthrough               *[]string    `yaml:"env_passthrough"`
	EnvPassthroughAll            *bool        `yaml:"env_passthrough_all"`
	HostMounts                   *[]HostMount `yaml:"host_mounts"`
}

// permissionsPatch holds permissions config fields that can be patched from YAML.
type permissionsPatch struct {
	Docker *bool `yaml:"docker"`
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
	Limits            *advancedLimitsPatch `yaml:"limits"`
	ReasoningEchoBack *bool                `yaml:"reasoning_echo_back"`
	Transport         *ModelTransportType  `yaml:"transport"`
	Reasoning         *reasoningPatch      `yaml:"reasoning"`
}

type advancedLimitsPatch struct {
	ContextWindow   *int `yaml:"context_window"`
	MaxOutputTokens *int `yaml:"max_output_tokens"`
}

type reasoningPatch struct {
	Effort           *string   `yaml:"effort"`
	SupportedEfforts *[]string `yaml:"supported_efforts"`
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
	ModelCallTimeout   *Duration            `yaml:"model_call_timeout"`
	ToolTimeoutDefault *Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       *map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes *int                 `yaml:"tool_output_max_bytes"`
	MaxParallelTools   *int                 `yaml:"max_parallel_tools"`
}

type subAgentPatch struct {
	Enabled     *bool `yaml:"enabled"`
	MaxTurns    *int  `yaml:"max_turns"`
	MaxTokens   *int  `yaml:"max_tokens"`
	MaxParallel *int  `yaml:"max_parallel"`
}

type advisorPatch struct {
	Enabled       *bool     `yaml:"enabled"`
	MaxUsesPerRun *int      `yaml:"max_uses_per_run"`
	MaxTokens     *int      `yaml:"max_tokens"`
	Timeout       *Duration `yaml:"timeout"`
}

type oneshotPatch struct {
	AutoPR *bool `yaml:"auto_pr"`
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
}

type projectContextPatch struct {
	MaxBytes    *int      `yaml:"max_bytes"`
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

type mcpPatch struct {
	Enabled *bool                      `yaml:"enabled"`
	Servers *map[string]mcpServerPatch `yaml:"servers"`
}

type mcpServerPatch struct {
	Enabled          *bool              `yaml:"enabled"`
	Transport        *string            `yaml:"transport"`
	Command          *string            `yaml:"command"`
	Args             *[]string          `yaml:"args"`
	Env              *map[string]string `yaml:"env"`
	URL              *string            `yaml:"url"`
	Headers          *map[string]string `yaml:"headers"`
	Approval         *string            `yaml:"approval"`
	TrustAnnotations *bool              `yaml:"trust_annotations"`
	ConnectTimeout   *Duration          `yaml:"connect_timeout"`
	AllowedTools     *[]string          `yaml:"allowed_tools"`
	BlockedTools     *[]string          `yaml:"blocked_tools"`
	SubAgents        *[]string          `yaml:"sub_agents"`
}

type modesPatch struct {
	Default *ExecutionMode `yaml:"default"`
}
