package output

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// APIRequestKindCompaction identifies a request used to compact context.
const APIRequestKindCompaction = "compaction"

// ModelCallStartedEvent marks the beginning of a provider request.
type ModelCallStartedEvent struct {
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

// ModelCallFinishedEvent records the outcome of a provider request.
type ModelCallFinishedEvent struct {
	Turn              int     `json:"turn"`
	Model             string  `json:"model,omitempty"`
	FinishReason      string  `json:"finish_reason,omitempty"`
	ToolCalls         int     `json:"tool_calls,omitempty"`
	CompletionTokens  int     `json:"completion_tokens,omitempty"`
	PromptTokens      int     `json:"prompt_tokens,omitempty"`
	CacheReadTokens   int     `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens int     `json:"cache_create_tokens,omitempty"`
	DurationMs        int64   `json:"duration_ms,omitempty"`
	TTFTMs            int64   `json:"ttft_ms,omitempty"`
	OutputTPS         float64 `json:"output_tps,omitempty"`
	Error             string  `json:"error,omitempty"`
}

// APIRequestEvent captures the provider request payload sent for a turn.
// Messages, Tools and Blocks are always populated in memory for in-process
// consumers (e.g. interactive.snapshotSink for context reporting), but
// MarshalJSON always strips them, substituting the bounded
// MessageCount/MessageHashes/*Bytes fields instead, so the exec-mode JSON
// stream and the session log stay bounded (one prior session produced 25MB
// from these fields alone). The only unbounded encoder is FileLogSink under
// diagnostics.capture_bodies; see apiRequestFull.
type APIRequestEvent struct {
	Model                 string                  `json:"model,omitempty"`
	Messages              []provider.Message      `json:"messages,omitempty"`
	Tools                 []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens             *int                    `json:"max_tokens,omitempty"`
	Blocks                []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget           prompt.ModelTokenBudget `json:"model_budget,omitempty"`
	EstimatedPromptTokens int                     `json:"estimated_prompt_tokens,omitempty"`
	RawPromptTokens       int                     `json:"raw_prompt_tokens,omitempty"`
	Kind                  string                  `json:"kind,omitempty"`
	Turn                  int                     `json:"turn,omitempty"`
	RequestID             string                  `json:"request_id,omitempty"`
	// MessageCount, MessageHashes, PromptBytes, ToolsBytes and BlocksBytes are
	// fixed-size scalars derived from Messages/Tools/Blocks so a parser can
	// spot prefix divergence and payload growth without the log carrying
	// message bodies. MessageHashes are computed over role, content and tool
	// call name/count (never tool call arguments or image data), so a
	// tool-call-only message still hashes distinctly from an empty one.
	// MessageCount/PromptBytes/ToolsBytes/BlocksBytes intentionally lack
	// omitempty: zero is a meaningful measurement (e.g. an empty request),
	// not an absent field.
	MessageCount  int      `json:"message_count"`
	MessageHashes []string `json:"message_hashes,omitempty"`
	PromptBytes   int      `json:"prompt_bytes"`
	ToolsBytes    int      `json:"tools_bytes"`
	BlocksBytes   int      `json:"blocks_bytes"`
}

// MarshalJSON bounds the encoded payload by dropping Messages, Tools and
// Blocks.
func (e APIRequestEvent) MarshalJSON() ([]byte, error) {
	out := apiRequestFull(e)
	out.Messages = nil
	out.Tools = nil
	out.Blocks = nil
	return json.Marshal(out)
}

// apiRequestFull is APIRequestEvent without its bounding MarshalJSON, so an
// encoder that has been granted diagnostics.capture_bodies can emit the full
// message, tool and block content. Conversion is the only way to opt out of
// the bound; nothing else in the codebase should define one.
type apiRequestFull APIRequestEvent

// APIResponseEvent captures the provider response payload for a turn.
type APIResponseEvent struct {
	Message      any    `json:"message,omitempty"`
	Usage        any    `json:"usage,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Error        string `json:"error,omitempty"`
	Turn         int    `json:"turn,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
}

// ProviderDiagnosticEvent describes provider retry and transport diagnostics.
type ProviderDiagnosticEvent struct {
	Turn           int    `json:"turn,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Suppressible   bool   `json:"suppressible,omitempty"`
	Message        string `json:"message,omitempty"`
	Attempt        int    `json:"attempt,omitempty"`
	MaxAttempts    int    `json:"max_attempts,omitempty"`
	Delay          string `json:"delay,omitempty"`
	Partial        bool   `json:"partial,omitempty"`
	TTFTMillis     int    `json:"ttft_millis,omitempty"`
	DurationMillis int    `json:"duration_millis,omitempty"`
}

// DiagnosticKindRetry is the ProviderDiagnosticEvent Kind for a transient
// provider retry. Attempt, MaxAttempts and Delay describe the retry.
const DiagnosticKindRetry = "retry"

// DiagnosticKindToolPanic is the ProviderDiagnosticEvent Kind for a tool call
// that panicked and was recovered into a tool error.
const DiagnosticKindToolPanic = "tool_panic"

// NewModelCallStartedEvent creates a new model call started event.
func NewModelCallStartedEvent(turn int, model string, messageCount int) Event {
	return newEvent(EventTypeModelCallStarted, ModelCallStartedEvent{
		Turn:         turn,
		Model:        model,
		MessageCount: messageCount,
	})
}

// ModelCallFinishedParams holds the arguments for NewModelCallFinishedEvent.
type ModelCallFinishedParams struct {
	Turn              int
	Model             string
	FinishReason      string
	ToolCalls         int
	CompletionTokens  int
	PromptTokens      int
	CacheReadTokens   int
	CacheCreateTokens int
	Err               error
	DurationMs        int64
	TTFTMs            int64
	OutputTPS         float64
}

// NewModelCallFinishedEvent creates a new model call finished event.
func NewModelCallFinishedEvent(p ModelCallFinishedParams) Event {
	payload := ModelCallFinishedEvent{
		Turn:              p.Turn,
		Model:             p.Model,
		FinishReason:      p.FinishReason,
		ToolCalls:         p.ToolCalls,
		CompletionTokens:  p.CompletionTokens,
		PromptTokens:      p.PromptTokens,
		CacheReadTokens:   p.CacheReadTokens,
		CacheCreateTokens: p.CacheCreateTokens,
		DurationMs:        p.DurationMs,
		TTFTMs:            p.TTFTMs,
		OutputTPS:         p.OutputTPS,
	}
	if p.Err != nil {
		payload.Error = p.Err.Error()
	}
	return newEvent(EventTypeModelCallFinished, payload)
}

// WithAPIRequestKind attaches a kind to an API request event.
func WithAPIRequestKind(event Event, kind string) Event {
	if kind == "" {
		return event
	}
	payload, ok := event.Payload.(APIRequestEvent)
	if !ok {
		return event
	}
	payload.Kind = kind
	event.Payload = payload
	return event
}

// WithAPICallIdentity attaches the turn number and per-model-call request id
// to an API request or response event, so a parser can pair a response with
// its request under concurrency. No-op for any other event type.
func WithAPICallIdentity(event Event, turn int, requestID string) Event {
	switch payload := event.Payload.(type) {
	case APIRequestEvent:
		payload.Turn = turn
		payload.RequestID = requestID
		event.Payload = payload
	case APIResponseEvent:
		payload.Turn = turn
		payload.RequestID = requestID
		event.Payload = payload
	}
	return event
}

// NewAPIRequestEvent creates a new API request event.
func NewAPIRequestEvent(model string, messages []provider.Message, tools []provider.ToolSpec, maxTokens *int, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget, estimatedPromptTokens, rawPromptTokens int) Event {
	messageHashes, promptBytes := requestMessageHashes(messages)
	toolsBytes := requestToolsBytes(tools)
	blocksBytes := 0
	for _, block := range blocks {
		blocksBytes += block.ByteSize
	}

	return newEvent(EventTypeAPIRequest, APIRequestEvent{
		Model:    model,
		Messages: provider.CloneMessages(messages),
		Tools:    provider.CloneTools(tools),
		MaxTokens: func() *int {
			if maxTokens == nil {
				return nil
			}
			cloned := *maxTokens
			return &cloned
		}(),
		Blocks:                append([]prompt.ContextBlock(nil), blocks...),
		ModelBudget:           budget,
		EstimatedPromptTokens: estimatedPromptTokens,
		RawPromptTokens:       rawPromptTokens,
		MessageCount:          len(messages),
		MessageHashes:         messageHashes,
		PromptBytes:           promptBytes,
		ToolsBytes:            toolsBytes,
		BlocksBytes:           blocksBytes,
	})
}

// requestMessageHashes returns the per-message fingerprints and the prompt
// byte count for an API request event. Fingerprints are built in one reused
// buffer and hashed in place, so a request never materializes a fingerprint
// string per message.
func requestMessageHashes(messages []provider.Message) ([]string, int) {
	hashes := make([]string, len(messages))
	var buf []byte
	promptBytes := 0
	for i, msg := range messages {
		promptBytes += len(msg.Role) + len(msg.Content)
		buf = provider.AppendMessageHashInput(buf[:0], msg)
		sum := sha256.Sum256(buf)
		hashes[i] = hex.EncodeToString(sum[:4])
	}
	return hashes, promptBytes
}

// requestToolsBytes returns the encoded size of the tool definitions, or zero
// when they cannot be marshaled. It is the ToolsBytes diagnostic only; the
// provider wire renders the tools separately.
func requestToolsBytes(tools []provider.ToolSpec) int {
	if len(tools) == 0 {
		return 0
	}
	toolsData, err := json.Marshal(tools)
	if err != nil {
		return 0
	}
	return len(toolsData)
}

// NewAPIResponseEvent creates a new API response event.
func NewAPIResponseEvent(message, usage any, finishReason string, err error) Event {
	payload := APIResponseEvent{
		Message:      message,
		Usage:        usage,
		FinishReason: finishReason,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return newEvent(EventTypeAPIResponse, payload)
}
