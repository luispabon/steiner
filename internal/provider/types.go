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
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Message is the provider-facing chat message envelope.
type Message struct {
	Role             MessageRole `json:"role"`
	Content          string      `json:"content,omitempty"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
	Name             string      `json:"name,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	Turn             int         `json:"turn,omitempty"`
}

// UsageStats carries token accounting returned by a provider.
type UsageStats struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// ChatRequest is the normalized provider request payload.
type ChatRequest struct {
	Model                 string         `json:"model"`
	Messages              []Message      `json:"messages"`
	MaxTokens             *int           `json:"max_tokens,omitempty"`
	Stream                bool           `json:"stream,omitempty"`
	Tools                 []ToolSpec     `json:"tools,omitempty"`
	Params                map[string]any `json:"-"` // Normalized generation params (temperature, top_p, etc.)
	ExtraParams           map[string]any `json:"-"` // Raw provider-specific passthrough
	IncludeEmptyReasoning bool           `json:"-"` // Fill empty reasoning_content on assistant messages before sending
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
