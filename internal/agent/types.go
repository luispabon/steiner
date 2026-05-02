package agent

type MessageRole string

const (
	MessageRoleUser         MessageRole = "user"
	MessageRoleAssistant    MessageRole = "assistant"
	MessageRoleTool         MessageRole = "tool"
	MessageRoleSummary      MessageRole = "summary"
	MessageRoleContextBlock MessageRole = "context-block"
)

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
	Source     string      `json:"source,omitempty"`
	ByteSize   int         `json:"byte_size,omitempty"`
	Turn       int         `json:"turn,omitempty"`
}
