package provider

// MessageRole identifies the provider-side role for a chat message.
type MessageRole string

const (
	// MessageRoleSystem identifies a system instruction message.
	MessageRoleSystem MessageRole = "system"
	// MessageRoleUser identifies an end-user message.
	MessageRoleUser MessageRole = "user"
	// MessageRoleAssistant identifies an assistant response message.
	MessageRoleAssistant MessageRole = "assistant"
	// MessageRoleTool identifies a tool result message.
	MessageRoleTool MessageRole = "tool"
)

// ToolFunctionSpec describes the callable portion of a tool schema.
type ToolFunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ToolSpec is the provider-facing schema for a callable tool.
type ToolSpec struct {
	Type     string           `json:"type"`
	Function ToolFunctionSpec `json:"function"`
}

// ToolCall records a model-requested tool invocation.
type ToolCall struct {
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	RawArguments string         `json:"raw_arguments,omitempty"`
}

// AnthropicMessageMetadata carries Anthropic-native replay fields that must be
// preserved on specific assistant messages.
type AnthropicMessageMetadata struct {
	ThinkingSignature string `json:"thinking_signature,omitempty"`
}

// CodexMessageMetadata carries Codex-native replay fields that must be
// preserved on specific assistant messages.
type CodexMessageMetadata struct {
	ReasoningID string `json:"reasoning_id,omitempty"`
}

// MessageProviderMetadata stores provider-native message fields needed for
// transport replay without changing prompt assembly semantics.
type MessageProviderMetadata struct {
	Anthropic *AnthropicMessageMetadata `json:"anthropic,omitempty"`
	Codex     *CodexMessageMetadata     `json:"codex,omitempty"`
}

// ImageBlock represents an image embedded in a message.
type ImageBlock struct {
	ID        string `json:"id,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
}

// Message is the provider-facing chat message envelope.
type Message struct {
	Role             MessageRole              `json:"role"`
	Content          string                   `json:"content,omitempty"`
	ReasoningContent string                   `json:"reasoning_content,omitempty"`
	Name             string                   `json:"name,omitempty"`
	ToolCallID       string                   `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall               `json:"tool_calls,omitempty"`
	Images           []ImageBlock             `json:"images,omitempty"`
	Turn             int                      `json:"turn,omitempty"`
	ProviderMetadata *MessageProviderMetadata `json:"provider_metadata,omitempty"`
}

// UsageStats carries token accounting returned by a provider.
type UsageStats struct {
	// PromptTokens is the TOTAL input token count for the turn, including any
	// cached portion (CacheReadInputTokens/CacheCreationInputTokens are not
	// additional to this total, they are subsets of it).
	PromptTokens             int `json:"prompt_tokens,omitempty"`
	CompletionTokens         int `json:"completion_tokens,omitempty"`
	TotalTokens              int `json:"total_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ChatRequest is the normalized provider request payload.
type ChatRequest struct {
	Model                 string            `json:"model"`
	Messages              []Message         `json:"messages"`
	MaxTokens             *int              `json:"max_tokens,omitempty"`
	Stream                bool              `json:"stream,omitempty"`
	Tools                 []ToolSpec        `json:"tools,omitempty"`
	PromptCacheKey        string            `json:"-"`
	Params                map[string]any    `json:"-"` // Normalized generation params (temperature, top_p, etc.)
	ExtraParams           map[string]any    `json:"-"` // Raw provider-specific passthrough
	IncludeEmptyReasoning bool              `json:"-"` // Fill empty reasoning_content on assistant messages before sending
	Reasoning             *ReasoningRequest `json:"-"` // Resolved reasoning effort; nil means provider default applies
	// AdvisorCacheProfile opts an Anthropic request into the advisor-shaped
	// cache profile: an extended 1h cache TTL on breakpoints, and breakpoint
	// placement redistributed across the reusable conversation tail instead
	// of wasting a breakpoint on the per-call unique final message. Defaults
	// to false for every other caller, leaving their requests unchanged.
	AdvisorCacheProfile bool `json:"-"`
}

// ChatResponse is the normalized provider response payload.
type ChatResponse struct {
	Message      Message     `json:"message"`
	Usage        *UsageStats `json:"usage,omitempty"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// ChatChunk is a streamed response fragment from a provider.
type ChatChunk struct {
	Delta         Message     `json:"delta"`
	Thinking      string      `json:"thinking,omitempty"`
	Usage         *UsageStats `json:"usage,omitempty"`
	Done          bool        `json:"done,omitempty"`
	FinishReason  string      `json:"finish_reason,omitempty"`
	Error         string      `json:"error,omitempty"`
	Diagnostic    string      `json:"diagnostic,omitempty"`
	Severity      string      `json:"severity,omitempty"`
	RetryReset    bool        `json:"retry_reset,omitempty"`
	OriginalError error       `json:"-"` // preserves the original error type (not serialized)
}
