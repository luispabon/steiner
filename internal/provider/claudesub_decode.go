package provider

import (
	"errors"
	"fmt"
	"strings"
)

// This file is the pure decoder for the claude CLI's `--output-format
// stream-json` lines. It turns one claudeSubEvent envelope into structured
// claudeSubDecoded events for the provider call boundary (step 6c). It owns no
// process, session, MCP host or policy state, and it imports no tool, agent or
// prompt code: MCP tool-name resolution and per-call host handles are injected
// as hooks.
//
// The CLI reports one assistant message up to three times, as the streamed
// `stream_event` deltas (raw Anthropic Messages SSE events), per-content-block
// `assistant` echoes, and the terminal `result`. To avoid double-counting, the
// decoder emits assistant text and thinking only from the streamed deltas,
// surfaces usage as cumulative `usage` events, attaches each message's final
// usage to its assembled message event, and never turns the
// `assistant` echo body or the `result` text into deltas. An echo for a streamed
// block confirms its tool_use identity and name; an echo-only tool_use is
// accepted only as the existing post-message_stop fallback. Echoes are
// idempotent across blocks, and the assembled message is emitted at
// message_stop once an in-stream echo has been processed, or at the post-stop
// echo or terminal result otherwise. Every content-block index belongs to a
// single streamed block start per message, and only a streamed tool_use block
// start authorises input_json_delta content. Each delta must name an index a
// streamed start already claimed and match that block's canonical type, and a
// stopped block rejects later deltas. A stop for an unclaimed index is ignored
// with a debug record; a second message_start and a success
// result before message_stop both fail closed, so no invalid ordering resets or drops
// accumulated content.
//
// The result envelope's usage covers the whole CLI query, including the
// tool-call messages that already carried their own usage. The decoder never
// sums or reconciles usage; claudeSubConsume reports the remainder on the final
// text chunk.

var (
	// errClaudeSubDecodeStream marks a malformed streamed envelope or
	// stream_event payload. It is a fail-closed error: losing a stream event
	// risks dropping assistant content, so the decoder never silently skips it.
	errClaudeSubDecodeStream = errors.New("decode claude_subscription stream event")
	// errClaudeSubResultEnvelope marks a malformed or unsupported terminal
	// result envelope. The decoder fails closed so an unexplained result is
	// never treated as a successful turn.
	errClaudeSubResultEnvelope = errors.New("malformed claude_subscription result envelope")
)

// claudeSubDecodeMaxToolInputBytes bounds one tool call's accumulated argument
// JSON. A single stdout line is already bounded by the process reader's
// scanner, but the number of argument deltas is not, so accumulation is capped
// and crossing the cap fails closed. It is a variable only so tests can lower
// it without building a multi-megabyte fixture.
var claudeSubDecodeMaxToolInputBytes = 64 << 20

// claudeSubDecodeMaxExcerptRunes bounds the raw text echoed in a decode error,
// so a malformed envelope cannot bloat a returned error or its log line.
const claudeSubDecodeMaxExcerptRunes = 200

// claudeSubDecodedKind classifies one decoded stream-json event.
type claudeSubDecodedKind int

const (
	// claudeSubDecodeText is an incremental assistant text delta.
	claudeSubDecodeText claudeSubDecodedKind = iota
	// claudeSubDecodeThinking is an incremental assistant thinking delta.
	claudeSubDecodeThinking
	// claudeSubDecodeToolUse is a tool_use observation, reported the moment the
	// CLI's stream announces the tool-use block.
	claudeSubDecodeToolUse
	// claudeSubDecodeUsage is a cumulative usage snapshot for the assistant
	// message currently being streamed.
	claudeSubDecodeUsage
	// claudeSubDecodeMessage is the assembled assistant message for one CLI
	// assistant message, emitted once the message's assistant echo has been
	// processed, or at the terminal result when no echo follows.
	claudeSubDecodeMessage
	// claudeSubDecodeResult is one CLI turn's terminal result envelope.
	claudeSubDecodeResult
)

// claudeSubDecoded is one decoded stream-json event. Only the fields relevant
// to Kind are set.
type claudeSubDecoded struct {
	Kind claudeSubDecodedKind
	// Text carries a text delta.
	Text string
	// Thinking carries a thinking delta.
	Thinking string
	// ToolUseID and ToolName carry a tool-use observation. ToolName is already
	// translated from the CLI's mcp__steiner__ form to the steiner tool name.
	ToolUseID string
	ToolName  string
	// Usage carries a cumulative per-message usage snapshot. On a
	// claudeSubDecodeMessage event it is the message's final usage.
	Usage *UsageStats
	// Message and FinishReason carry the assembled assistant message.
	Message      *Message
	FinishReason string
	// UUID is the final CLI transcript uuid of the assembled message: the
	// top-level uuid of the last assistant echo accepted before emission. Empty
	// when no such echo carried one.
	UUID string
	// Result carries terminal turn metadata.
	Result *claudeSubResult
}

// claudeSubResult is the terminal metadata of one CLI turn's result envelope.
type claudeSubResult struct {
	// Subtype is the CLI result subtype ("success", "error_during_execution",
	// "error_max_turns", ...).
	Subtype string
	// IsError reports a non-successful terminal result.
	IsError bool
	// Text is the result envelope's final assistant text. It is metadata only:
	// the decoder never re-emits it as a text delta.
	Text string
	// NumTurns, StopReason and TotalCostUSD mirror the envelope fields.
	NumTurns     int
	StopReason   string
	TotalCostUSD float64
	// PermissionDenials counts the envelope's permission_denials entries.
	PermissionDenials int
	// Usage is the turn-cumulative usage (all assistant messages), distinct
	// from each message's own usage events.
	Usage *UsageStats
}

// claudeSubDecodeHooks are the caller-supplied callbacks the decoder uses while
// decoding. They keep MCP name resolution and opaque host call handles in the
// provider orchestration layer instead of the decoder.
type claudeSubDecodeHooks struct {
	// ToolName maps a CLI-published tool name (for example
	// "mcp__steiner__read") to the steiner tool name. It must return the input
	// unchanged when the name is unknown.
	ToolName func(cliName string) string
	// ObserveToolUse is invoked once per tool_use the stream announces, with
	// the tool-use id and the translated steiner name, before the call's result
	// can be resolved. It may be nil.
	ObserveToolUse func(id, steinerName string)
}

// claudeSubDecoder is the stateful stream-json decoder. One decoder follows one
// CLI process for its lifetime; per-message state is reset at each
// message_start.
type claudeSubDecoder struct {
	hooks claudeSubDecodeHooks

	content     strings.Builder
	thinking    strings.Builder
	signature   string
	sawContent  bool
	sawThinking bool
	toolUses    map[int]*anthropicToolUseAccumulator
	sawToolUse  bool
	stopReason  string
	usage       *claudeSubUsage

	// blocks maps each streamed content_block_start index to the canonical
	// block type the decoder consumed for it ("text", "thinking" or
	// "tool_use"). Every streamed start must claim a fresh index, so a text or
	// thinking block index can never be reused for a tool use (or the reverse),
	// and an echo-only tool use can never borrow a streamed index as stream
	// authority. Reset at each message_start.
	blocks map[int]string

	// echoToolIndex hands out strictly negative accumulator keys to tool uses
	// the assistant echo introduces that the stream did not observe. Streamed
	// content-block indices are validated non-negative, so an echo-only
	// accumulator can never collide with a streamed block.
	echoToolIndex int

	// observed holds the tool-use ids already announced for the current message,
	// so the streamed block and the later assistant echo observe each id once.
	observed map[string]struct{}

	// toolOrder records the canonical observation order of the current
	// message's accepted tool uses: a streamed tool_use block start appends its
	// content-block index as the stream announces it, and an echo-only fallback
	// appends its accumulator key at its echo appearance. An echo that only
	// confirms an existing identity appends nothing. Final assembly follows
	// this order so a late echo-only fallback, whose synthetic key is negative,
	// is not sorted ahead of the streamed calls that preceded it. Reset at each
	// message_start; every accepted tool use appears once with no duplicate.
	toolOrder []int

	// unparsedToolInputs records stream-authorised CLI sentinel arguments by
	// accumulator key. The raw input is preserved for the final ToolCall instead
	// of being repaired or passed through strict JSON parsing.
	unparsedToolInputs map[int]string

	// phase is where the decoder is in the turn's stream-json sequence. It
	// bounds which later envelopes may still mutate state or invoke callbacks.
	phase claudeSubDecodePhase
	// messagePending records that the stopped message's final assistant message
	// is being held for emission. An in-stream echo allows the message to flush
	// at message_stop; a post-stop fallback echo or the terminal result flushes
	// it otherwise.
	messagePending bool
	// echoProcessed records that at least one valid assistant echo was processed
	// for this message. Repeated per-content-block echoes are accepted
	// idempotently rather than treated as duplicate messages.
	echoProcessed bool
	// streamEchoProcessed records that a valid echo arrived before message_stop.
	// It is what permits finishMessage to emit the assembled message immediately.
	streamEchoProcessed bool
	// messageUUID is the top-level uuid of the last assistant echo accepted while
	// the current message was still pending emission: its final CLI transcript
	// entry. Reset at message_start.
	messageUUID string
	// messageStarted records that this decoder's single message_start has been
	// accepted. One decoder follows one CLI turn, which carries exactly one
	// message_start, so a second one fails closed instead of silently resetting
	// the in-flight message's content. Only a new decoder clears it.
	messageStarted bool

	// stopped tracks the streamed content-block indices whose content_block_stop
	// has been accepted. A claimed block may stop once; a repeated stop, or a
	// delta for an already stopped block, fails closed. Reset at each
	// message_start.
	stopped map[int]bool
}

// claudeSubDecodePhase is where the decoder is in one CLI turn's stream-json
// sequence. message_stop closes the streamed message and the terminal result
// closes the turn, so each phase bounds which later envelopes may still mutate
// accumulated state or invoke a hook.
type claudeSubDecodePhase int

const (
	// claudeSubPhaseStreaming accepts streamed content, deltas and tool uses
	// for the message the stream is currently delivering. It is the zero value
	// so a decoder accepts the envelopes a caller forwards from mid-stream.
	claudeSubPhaseStreaming claudeSubDecodePhase = iota
	// claudeSubPhaseStopped follows message_stop: the streamed message is
	// closed and only the assistant echo may still add a fallback tool use
	// before the terminal result. Every other known stream event is rejected.
	claudeSubPhaseStopped
	// claudeSubPhaseTerminal follows the terminal result: nothing may mutate
	// state or invoke a hook after it, so every known stream event and every
	// assistant echo is rejected.
	claudeSubPhaseTerminal
)

// newClaudeSubDecoder returns a decoder that uses hooks while decoding.
func newClaudeSubDecoder(hooks claudeSubDecodeHooks) *claudeSubDecoder {
	return &claudeSubDecoder{hooks: hooks, observed: map[string]struct{}{}}
}

// decode converts one CLI stream-json envelope into decoded events. Unknown or
// irrelevant envelope types yield no events; a malformed stream event or a
// malformed or unsupported result envelope is a fail-closed error. A result
// envelope that reports a terminal error is surfaced as an event, not an error:
// the decoder does not decide policy.
func (d *claudeSubDecoder) decode(ev claudeSubEvent) ([]claudeSubDecoded, error) {
	switch ev.Type {
	case "stream_event":
		return d.decodeStreamEvent(ev.Raw)
	case "assistant":
		// Assistant echoes identify individual content blocks and can arrive
		// while the stream is still delivering later blocks. During streaming,
		// only already-announced tool uses are valid; the echo-only fallback is
		// retained for the stopped phase. After the terminal result, a rejection
		// mutates nothing and calls no hook.
		switch d.phase {
		case claudeSubPhaseStreaming:
			return d.decodeAssistantEcho(ev.Raw, ev.UUID, false)
		case claudeSubPhaseStopped:
			return d.decodeAssistantEcho(ev.Raw, ev.UUID, d.messagePending)
		case claudeSubPhaseTerminal:
			return nil, fmt.Errorf("%w: assistant echo after terminal result", errClaudeSubDecodeStream)
		default:
			return nil, fmt.Errorf("%w: assistant echo in invalid phase", errClaudeSubDecodeStream)
		}
	case "result":
		if d.phase == claudeSubPhaseTerminal {
			return nil, fmt.Errorf("%w: duplicate terminal result", errClaudeSubResultEnvelope)
		}
		return d.decodeResult(ev.Raw)
	default:
		// Unrelated top-level, control and system envelopes carry nothing the
		// decoder needs and are ignored in every phase.
		return nil, nil
	}
}

// claudeSubUsage is an Anthropic usage object with field presence preserved.
// The CLI splits a message's usage across snapshots and omits the components a
// given snapshot does not report, so an absent cache field must not be read as
// an explicit zero that clears an earlier value.
type claudeSubUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
}

// toUsageStats converts a presence-aware usage object to UsageStats. Prompt
// tokens sum the Anthropic input components that are present, matching the
// anthropic provider's toUsageStats.
func (u *claudeSubUsage) toUsageStats() *UsageStats {
	if u == nil {
		return nil
	}
	stats := &UsageStats{}
	if u.InputTokens != nil {
		stats.PromptTokens += *u.InputTokens
	}
	if u.CacheCreationInputTokens != nil {
		stats.PromptTokens += *u.CacheCreationInputTokens
		stats.CacheCreationInputTokens = *u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens != nil {
		stats.PromptTokens += *u.CacheReadInputTokens
		stats.CacheReadInputTokens = *u.CacheReadInputTokens
	}
	if u.OutputTokens != nil {
		stats.CompletionTokens = *u.OutputTokens
	}
	stats.TotalTokens = stats.PromptTokens + stats.CompletionTokens
	return stats
}
