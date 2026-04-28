package provider

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

type ToolFunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type ToolSpec struct {
	Type     string           `json:"type"`
	Function ToolFunctionSpec `json:"function"`
}

type ToolCall struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type Message struct {
	Role       MessageRole `json:"role"`
	Content    string      `json:"content,omitempty"`
	Name       string      `json:"name,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
}

type UsageStats struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type ChatRequest struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	Temperature *float64   `json:"temperature,omitempty"`
	MaxTokens   *int       `json:"max_tokens,omitempty"`
	TopP        *float64   `json:"top_p,omitempty"`
	Stream      bool       `json:"stream,omitempty"`
	Tools       []ToolSpec `json:"tools,omitempty"`
}

type ChatResponse struct {
	Message      Message     `json:"message"`
	Usage        *UsageStats `json:"usage,omitempty"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type ChatChunk struct {
	Delta         Message     `json:"delta"`
	Thinking      string      `json:"thinking,omitempty"`
	Usage         *UsageStats `json:"usage,omitempty"`
	Done          bool        `json:"done,omitempty"`
	FinishReason  string      `json:"finish_reason,omitempty"`
	Error         string      `json:"error,omitempty"`
	OriginalError error       `json:"-"` // preserves the original error type (not serialized)
}
