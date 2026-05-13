package agent

// MessageRole identifies the semantic role of a conversation message.
type MessageRole string

const (
	MessageRoleUser         MessageRole = "user"
	MessageRoleAssistant    MessageRole = "assistant"
	MessageRoleTool         MessageRole = "tool"
	MessageRoleSummary      MessageRole = "summary"
	MessageRoleContextBlock MessageRole = "context-block"
)

// ToolCall records a requested tool invocation.
type ToolCall struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type MessageRetention struct {
	Kind       string `json:"kind,omitempty"`
	Summary    string `json:"summary,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Status     string `json:"status,omitempty"`
	TurnCount  int    `json:"turn_count,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`
}

// Message is the agent-side conversation record used across compaction flows.
type Message struct {
	Role       MessageRole       `json:"role"`
	Content    string            `json:"content,omitempty"`
	Name       string            `json:"name,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall        `json:"tool_calls,omitempty"`
	Source     string            `json:"source,omitempty"`
	ByteSize   int               `json:"byte_size,omitempty"`
	Turn       int               `json:"turn,omitempty"`
	Retention  *MessageRetention `json:"retention,omitempty"`
}
