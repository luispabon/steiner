package agent

// MessageRole identifies the semantic role of a conversation message.
type MessageRole string

const (
	// MessageRoleUser identifies end-user input.
	MessageRoleUser MessageRole = "user"
	// MessageRoleAssistant identifies agent-authored content.
	MessageRoleAssistant MessageRole = "assistant"
	// MessageRoleTool identifies a tool result message.
	MessageRoleTool MessageRole = "tool"
	// MessageRoleSummary identifies a compaction summary.
	MessageRoleSummary MessageRole = "summary"
	// MessageRoleContextBlock identifies an injected context block.
	MessageRoleContextBlock MessageRole = "context-block"
)

// ToolCall records a requested tool invocation.
type ToolCall struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// MessageRetention describes summary metadata retained across compaction.
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
