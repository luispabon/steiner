package output

// AssistantMessageEvent records a completed assistant message.
type AssistantMessageEvent struct {
	Turn    int    `json:"turn,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ChunkSource identifies the origin of a streamed assistant or thinking chunk.
type ChunkSource string

const (
	// ChunkSourceAssistant identifies chunks emitted from assistant output.
	ChunkSourceAssistant ChunkSource = "assistant"
	// ChunkSourceAdvisor identifies chunks emitted by the stronger-model advisor.
	ChunkSourceAdvisor ChunkSource = "advisor"
)

// AssistantChunkEvent records a streamed assistant chunk.
type AssistantChunkEvent struct {
	Turn    int         `json:"turn,omitempty"`
	Content string      `json:"content,omitempty"`
	Source  ChunkSource `json:"source,omitempty"`
}

// ThinkingChunkEvent records a streamed reasoning chunk.
type ThinkingChunkEvent struct {
	Turn    int         `json:"turn,omitempty"`
	Content string      `json:"content,omitempty"`
	Source  ChunkSource `json:"source,omitempty"`
}

// NewAssistantMessageEvent creates a new assistant message event.
func NewAssistantMessageEvent(turn int, role, content string) Event {
	return newEvent(EventTypeAssistantMessage, AssistantMessageEvent{
		Turn:    turn,
		Role:    role,
		Content: content,
	})
}

// NewThinkingChunkEventWithSource creates a new thinking chunk event with an
// explicit chunk source.
func NewThinkingChunkEventWithSource(turn int, content string, source ChunkSource) Event {
	return newEvent(EventTypeThinkingChunk, ThinkingChunkEvent{
		Turn:    turn,
		Content: content,
		Source:  source,
	})
}

// NewAssistantChunkEventWithSource creates a new assistant chunk event with an
// explicit chunk source.
func NewAssistantChunkEventWithSource(turn int, content string, source ChunkSource) Event {
	return newEvent(EventTypeAssistantChunk, AssistantChunkEvent{
		Turn:    turn,
		Content: content,
		Source:  source,
	})
}
