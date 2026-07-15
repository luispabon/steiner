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
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	RawArguments string         `json:"raw_arguments,omitempty"`
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

// SteerMessage carries a between-turn steering message with attached images.
type SteerMessage struct {
	Text   string
	Images []ImageBlock
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

// Message is the agent-side conversation record used across compaction flows.
type Message struct {
	Role             MessageRole              `json:"role"`
	Content          string                   `json:"content,omitempty"`
	ReasoningContent string                   `json:"reasoning_content,omitempty"`
	Name             string                   `json:"name,omitempty"`
	ToolCallID       string                   `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall               `json:"tool_calls,omitempty"`
	Images           []ImageBlock             `json:"images,omitempty"`
	Source           string                   `json:"source,omitempty"`
	ByteSize         int                      `json:"byte_size,omitempty"`
	Turn             int                      `json:"turn,omitempty"`
	Retention        *MessageRetention        `json:"retention,omitempty"`
	ProviderMetadata *MessageProviderMetadata `json:"provider_metadata,omitempty"`
}
