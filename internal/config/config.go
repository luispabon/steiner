package config

// ExecutionMode defines the execution mode for plan or build behavior.
type ExecutionMode string

const (
	// ExecutionModePlan sets execution mode to plan.
	ExecutionModePlan ExecutionMode = "plan"
	// ExecutionModeBuild sets execution mode to build.
	ExecutionModeBuild ExecutionMode = "build"
)

// ModesConfig holds execution mode configuration.
type ModesConfig struct {
	Default ExecutionMode `yaml:"default"`
}

// ContextManagementConfig holds baseline context management settings.
type ContextManagementConfig struct {
	ReadAnnotations bool `yaml:"read_annotations"`
}

// ProviderType is the type of model provider.
type ProviderType string

const (
	// ProviderTypeOpenAICompat targets OpenAI-compatible HTTP APIs.
	ProviderTypeOpenAICompat ProviderType = "openai_compat"
	// ProviderTypeOllama targets the Ollama API.
	ProviderTypeOllama ProviderType = "ollama"
	// ProviderTypeLMStudio targets LM Studio's OpenAI-compatible API.
	ProviderTypeLMStudio ProviderType = "lmstudio"
	// ProviderTypeOpenRouter targets the OpenRouter API.
	ProviderTypeOpenRouter ProviderType = "openrouter"
	// ProviderTypeOpenAI targets the native OpenAI API.
	ProviderTypeOpenAI ProviderType = "openai"
	// ProviderTypeAnthropic targets the native Anthropic API.
	ProviderTypeAnthropic ProviderType = "anthropic"
	// ProviderTypeGemini targets the native Gemini API.
	ProviderTypeGemini ProviderType = "gemini"
	// ProviderTypeLiteLLM targets a LiteLLM gateway endpoint.
	ProviderTypeLiteLLM ProviderType = "litellm"
	// ProviderTypeCodex targets the OpenAI Codex API via OAuth.
	ProviderTypeCodex ProviderType = "codex"
)

// CodexConfig configures Codex OAuth provider-specific behavior.
type CodexConfig struct {
	MinRequestInterval Duration `yaml:"min_request_interval"`
}

// ProviderConfig configures a model provider.
type ProviderConfig struct {
	Type      ProviderType      `yaml:"type"`
	BaseURL   string            `yaml:"base_url"`
	APIKey    string            `yaml:"api_key"`
	APIKeyEnv string            `yaml:"api_key_env"`
	Headers   map[string]string `yaml:"headers"`
	Timeout   Duration          `yaml:"timeout"`
	Codex     CodexConfig       `yaml:"codex"`
}

// AdvancedLimitsConfig defines token limits for model inference.
type AdvancedLimitsConfig struct {
	ContextWindow   int `yaml:"context_window"`
	MaxOutputTokens int `yaml:"max_output_tokens"`
}

// ModelTransportType controls how Steiner chooses the request transport for a model.
type ModelTransportType string

const (
	// ModelTransportAuto uses models.dev metadata when available, else the configured provider type.
	ModelTransportAuto ModelTransportType = "auto"
	// ModelTransportOpenAICompat forces OpenAI-compatible request transport.
	ModelTransportOpenAICompat ModelTransportType = "openai_compat"
	// ModelTransportAnthropic forces Anthropic-native request transport.
	ModelTransportAnthropic ModelTransportType = "anthropic"
)

// AdvancedConfig holds advanced model-specific configuration.
type AdvancedConfig struct {
	Limits            AdvancedLimitsConfig `yaml:"limits"`
	ReasoningEchoBack *bool                `yaml:"reasoning_echo_back"`
	Transport         ModelTransportType   `yaml:"transport"`
	Reasoning         ReasoningConfig      `yaml:"reasoning"`
}

// ReasoningConfig configures model reasoning effort. Values are
// provider/model-native strings (e.g. "low", "high", "xhigh") and are not
// normalized into a Steiner-owned enum.
type ReasoningConfig struct {
	Effort           string   `yaml:"effort"`
	SupportedEfforts []string `yaml:"supported_efforts"`
}

// SearchConfig configures web search integration.
type SearchConfig struct {
	Backend      string `yaml:"backend"`
	SearxngURL   string `yaml:"searxng_url"`
	GoogleCx     string `yaml:"google_cx"`
	GoogleAPIKey string `yaml:"google_api_key"`
	KagiAPIKey   string `yaml:"kagi_api_key"`
	BraveAPIKey  string `yaml:"brave_api_key"`
}

// MCPConfig configures Model Context Protocol servers.
type MCPConfig struct {
	Enabled bool                       `yaml:"enabled"`
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

// MCPServerConfig declares one MCP server.
type MCPServerConfig struct {
	Enabled          bool              `yaml:"enabled"`
	Transport        string            `yaml:"transport"`
	Command          string            `yaml:"command"`
	Args             []string          `yaml:"args"`
	Env              map[string]string `yaml:"env"`
	URL              string            `yaml:"url"`
	Headers          map[string]string `yaml:"headers"`
	Approval         string            `yaml:"approval"`
	TrustAnnotations bool              `yaml:"trust_annotations"`
	ConnectTimeout   Duration          `yaml:"connect_timeout"`
	AllowedTools     []string          `yaml:"allowed_tools"`
	BlockedTools     []string          `yaml:"blocked_tools"`
	SubAgents        []string          `yaml:"sub_agents"`
}

// SandboxConfig controls bubblewrap sandbox behaviour for tool execution.
type SandboxConfig struct {
	Enabled                      bool     `yaml:"enabled"`
	WarningOnUnsupportedPlatform bool     `yaml:"warning_on_unsupported_platform"`
	EnvPassthrough               []string `yaml:"env_passthrough"`
	EnvPassthroughAll            bool     `yaml:"env_passthrough_all"`
}

// PermissionsConfig holds host-capability flags granted to the sandbox.
type PermissionsConfig struct {
	Docker bool `yaml:"docker"`
}

// HostMount describes an additional host path mounted inside the sandbox.
type HostMount struct {
	Path string `yaml:"path"`
	Mode string `yaml:"mode"` // "ro" or "rw"
}

// Config is the complete application configuration.
type Config struct {
	Scheduler            SchedulerConfig            `yaml:"scheduler"`
	Providers            map[string]ProviderConfig  `yaml:"providers"`
	Models               ModelsConfig               `yaml:"models"`
	Limits               LimitsConfig               `yaml:"limits"`
	Sandbox              SandboxConfig              `yaml:"sandbox"`
	Permissions          PermissionsConfig          `yaml:"permissions"`
	HostMounts           []HostMount                `yaml:"host_mounts"`
	SubAgent             SubAgentConfig             `yaml:"sub_agent"`
	Advisor              AdvisorConfig              `yaml:"advisor"`
	OneShot              oneshotConfig              `yaml:"oneshot"`
	DesktopNotifications desktopNotificationsConfig `yaml:"desktop_notifications"`
	Tools                map[string]ToolConfig      `yaml:"tools"`
	ProjectContext       ProjectContextConfig       `yaml:"project_context"`
	Paths                PathsConfig                `yaml:"paths"`
	Logging              LoggingConfig              `yaml:"logging"`
	ContextManagement    ContextManagementConfig    `yaml:"context_management"`
	CaveHuman            bool                       `yaml:"cave_human"`
	Search               SearchConfig               `yaml:"search"`
	MCP                  MCPConfig                  `yaml:"mcp"`
	Modes                ModesConfig                `yaml:"modes"`
	TUI                  TUIConfig                  `yaml:"tui"`
}

// TUIConfig configures the interactive terminal UI.
type TUIConfig struct {
	// FPS is the renderer frame rate. Bubble Tea flushes the terminal on this
	// ticker, so it bounds how long a processed keystroke waits before it is
	// visible: the average wait is half a frame. Raising it reduces that wait
	// and increases CPU proportionally, because renderer flush dominates TUI
	// CPU cost. Valid range 1-120; Bubble Tea caps above 120.
	FPS int `yaml:"fps"`
}

// ModelsConfig consolidates all model configuration: the model definitions
// themselves and the role-based aliases that reference them.
type ModelsConfig struct {
	Default         string                 `yaml:"default"`
	Definitions     map[string]ModelConfig `yaml:"definitions"`
	Advisor         string                 `yaml:"advisor"`
	SubAgents       map[string]string      `yaml:"sub_agents"`
	OneShot         map[string]string      `yaml:"oneshot"`
	WorkflowHandoff map[string]string      `yaml:"workflow_handoff"`
}

// SchedulerConfig controls provider concurrency.
type SchedulerConfig struct {
	Parallelism int `yaml:"parallelism"`
}

// ModelConfig configures a model instance.
type ModelConfig struct {
	Provider     string         `yaml:"provider"`
	ID           string         `yaml:"id"`
	Params       map[string]any `yaml:"params"`
	ExtraParams  map[string]any `yaml:"extra_params"`
	PromptSuffix string         `yaml:"prompt_suffix"`
	Retry        RetryConfig    `yaml:"retry"`
	Prompts      ModelPrompts   `yaml:"prompts"`
	Advanced     AdvancedConfig `yaml:"advanced"`
	Vision       *bool          `yaml:"vision"`
}

// RetryConfig controls retry behaviour for model requests.
type RetryConfig struct {
	Enabled        bool     `yaml:"enabled"`
	MaxAttempts    int      `yaml:"max_attempts"`
	InitialBackoff Duration `yaml:"initial_backoff"`
	MaxBackoff     Duration `yaml:"max_backoff"`
	RetryAfterMax  Duration `yaml:"retry_after_max"`
}

// ModelPrompts contains per-model prompt overrides. These override the
// embedded default prompts and are only settable via config file.
type ModelPrompts struct {
	System       string `yaml:"system"`
	Compaction   string `yaml:"compaction"`
	SystemSuffix string `yaml:"system_suffix,omitempty"`
}

// LimitsConfig defines runtime limits for turns, tokens, and tools.
type LimitsConfig struct {
	MaxTurns           int                 `yaml:"max_turns"`
	MaxTokens          int                 `yaml:"max_tokens"`
	ToolTimeoutDefault Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes int                 `yaml:"tool_output_max_bytes"`
}

// SubAgentConfig controls delegated child-agent execution limits.
type SubAgentConfig struct {
	Enabled   bool `yaml:"enabled"`
	MaxTurns  int  `yaml:"max_turns"`
	MaxTokens int  `yaml:"max_tokens"`
}

// AdvisorConfig controls the optional advisor reasoning pass.
type AdvisorConfig struct {
	Enabled       bool      `yaml:"enabled"`
	MaxUsesPerRun int       `yaml:"max_uses_per_run"`
	MaxTokens     *int      `yaml:"max_tokens"`
	Timeout       *Duration `yaml:"timeout"`
}

type oneshotConfig struct {
	AutoPR bool `yaml:"auto_pr"`
}

type desktopNotificationsConfig struct {
	Enabled  bool `yaml:"enabled"`
	Duration int  `yaml:"duration"`
}

// ToolConfig defines an externally configured tool.
type ToolConfig struct {
	Exec        string         `yaml:"exec"`
	Subcommand  string         `yaml:"subcommand"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	Timeout     Duration       `yaml:"timeout"`
	Constraints map[string]any `yaml:"constraints"`
}

// ProjectContextConfig defines extra project files included in prompts.
type ProjectContextConfig struct {
	// MaxBytes is the byte budget for extra project context files.
	MaxBytes int `yaml:"max_bytes"`
	// MaxTokens is deprecated; use MaxBytes. When set and MaxBytes is unset,
	// it is converted to bytes as MaxTokens * 4 at load time.
	MaxTokens   int      `yaml:"max_tokens,omitempty"`
	ExtraFiles  []string `yaml:"extra_files"`
	IgnoreFiles []string `yaml:"ignore_files"`
}

// PathsConfig constrains filesystem access for tools.
type PathsConfig struct {
	ProjectRootOnly bool     `yaml:"project_root_only"`
	WritablePaths   []string `yaml:"writable_paths"`
	BlockedPaths    []string `yaml:"blocked_paths"`
	ExcludePaths    []string `yaml:"exclude_paths"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
}

// LoggingConfig controls diagnostic log output.
type LoggingConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Level             string `yaml:"level"`
	File              string `yaml:"file"`
	ThinkingChunk     bool   `yaml:"thinking_chunk"`
	CompactionLogFile string `yaml:"compaction_log_file"`
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

// copyStringSlice creates a shallow copy of a []string.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}
