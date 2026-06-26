package config

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

// ProviderConfig configures a model provider.
type ProviderConfig struct {
	Type      ProviderType      `yaml:"type"`
	BaseURL   string            `yaml:"base_url"`
	APIKey    string            `yaml:"api_key"`
	APIKeyEnv string            `yaml:"api_key_env"`
	Headers   map[string]string `yaml:"headers"`
	Timeout   Duration          `yaml:"timeout"`
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

// SandboxConfig controls bubblewrap sandbox behaviour for tool execution.
type SandboxConfig struct {
	Enabled bool `yaml:"enabled"`
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
	DefaultModel         string                     `yaml:"default_model"`
	Providers            map[string]ProviderConfig  `yaml:"providers"`
	Models               map[string]ModelConfig     `yaml:"models"`
	Limits               LimitsConfig               `yaml:"limits"`
	Sandbox              SandboxConfig              `yaml:"sandbox"`
	Permissions          PermissionsConfig          `yaml:"permissions"`
	HostMounts           []HostMount                `yaml:"host_mounts"`
	SubAgent             SubAgentConfig             `yaml:"sub_agent"`
	Advisor              AdvisorConfig              `yaml:"advisor"`
	OneShot              oneshotConfig              `yaml:"oneshot"`
	WorkflowHandoff      workflowHandoffConfig      `yaml:"workflow_handoff"`
	DesktopNotifications desktopNotificationsConfig `yaml:"desktop_notifications"`
	Tools                map[string]ToolConfig      `yaml:"tools"`
	ProjectContext       ProjectContextConfig       `yaml:"project_context"`
	Paths                PathsConfig                `yaml:"paths"`
	Logging              LoggingConfig              `yaml:"logging"`
	ContextManagement    ContextManagementConfig    `yaml:"context_management"`
	CaveHuman            bool                       `yaml:"cave_human"`
	Search               SearchConfig               `yaml:"search"`
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

// AgentConfig holds per-agent-type configuration.
type AgentConfig struct {
	// Model is an optional model alias override for this agent type.
	Model string `yaml:"model"`
}

// SubAgentConfig controls delegated child-agent execution limits.
type SubAgentConfig struct {
	Enabled      bool                   `yaml:"enabled"`
	MaxTurns     int                    `yaml:"max_turns"`
	MaxTokens    int                    `yaml:"max_tokens"`
	AllowedTools []string               `yaml:"allowed_tools"`
	Agents       map[string]AgentConfig `yaml:"agents"`
}

// AdvisorConfig controls the optional advisor reasoning pass.
type AdvisorConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Model         string `yaml:"model"`
	MaxUsesPerRun int    `yaml:"max_uses_per_run"`
	MaxTokens     *int   `yaml:"max_tokens"`
}

type workflowHandoffConfig struct {
	Models map[string]string `yaml:"models"`
}

type oneshotConfig struct {
	Models map[string]string `yaml:"models"`
	AutoPR bool              `yaml:"auto_pr"`
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
	MaxTokens   int      `yaml:"max_tokens"`
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
