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

// AdvancedConfig holds advanced model-specific configuration.
type AdvancedConfig struct {
	Limits            AdvancedLimitsConfig `yaml:"limits"`
	ReasoningEchoBack *bool                `yaml:"reasoning_echo_back"`
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

// Config is the complete application configuration.
type Config struct {
	Scheduler         SchedulerConfig           `yaml:"scheduler"`
	DefaultModel      string                    `yaml:"default_model"`
	Providers         map[string]ProviderConfig `yaml:"providers"`
	Models            map[string]ModelConfig    `yaml:"models"`
	Limits            LimitsConfig              `yaml:"limits"`
	Approval          ApprovalConfig            `yaml:"approval"`
	SubAgent          SubAgentConfig            `yaml:"sub_agent"`
	Tools             map[string]ToolConfig     `yaml:"tools"`
	ProjectContext    ProjectContextConfig      `yaml:"project_context"`
	Paths             PathsConfig               `yaml:"paths"`
	Logging           LoggingConfig             `yaml:"logging"`
	Debug             DebugConfig               `yaml:"debug"`
	ContextManagement ContextManagementConfig   `yaml:"context_management"`
	CavemanMode       bool                      `yaml:"caveman_mode"`
	Search            SearchConfig              `yaml:"search"`
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
	System     string `yaml:"system"`
	Compaction string `yaml:"compaction"`
}

// LimitsConfig defines runtime limits for turns, tokens, and tools.
type LimitsConfig struct {
	MaxTurns           int                 `yaml:"max_turns"`
	MaxTokens          int                 `yaml:"max_tokens"`
	ToolTimeoutDefault Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes int                 `yaml:"tool_output_max_bytes"`
}

// ApprovalMode defines the default approval policy for tool execution.
type ApprovalMode string

const (
	// ApprovalModeAuto allows tools according to automatic policy.
	ApprovalModeAuto ApprovalMode = "auto"
	// ApprovalModePrompt asks for user approval before running gated tools.
	ApprovalModePrompt ApprovalMode = "prompt"
	// ApprovalModeDeny blocks gated tools.
	ApprovalModeDeny ApprovalMode = "deny"
)

// ApprovalConfig controls default and per-tool approval behaviour.
type ApprovalConfig struct {
	Default       ApprovalMode             `yaml:"default"`
	ToolOverrides map[string]*ApprovalMode `yaml:"tool_overrides"`
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

// ToolConfig defines an externally configured tool.
type ToolConfig struct {
	Exec        string         `yaml:"exec"`
	Subcommand  string         `yaml:"subcommand"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	Timeout     Duration       `yaml:"timeout"`
	Approval    ApprovalMode   `yaml:"approval"`
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

// DebugConfig exposes internal debugging toggles.
type DebugConfig struct {
	ShowInternalScaffoldInference bool `yaml:"show_internal_scaffold_inference"`
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
