package provider

import (
	"encoding/json"
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
// surfaces usage only as cumulative `usage` events, and never turns the
// `assistant` echo body or the `result` text into deltas. An echo for a streamed
// block confirms its tool_use identity and name; an echo-only tool_use is
// accepted only as the existing post-message_stop fallback. Echoes are
// idempotent across blocks, and the assembled message is emitted at
// message_stop once an in-stream echo has been processed, or at the post-stop
// echo or terminal result otherwise. Every content-block index belongs to a
// single streamed block start per message, and only a streamed tool_use block
// start authorises input_json_delta content. Each delta and stop must name an
// index a streamed start already claimed and match that block's canonical type,
// and a stopped block rejects later deltas; a second message_start and a success
// result before message_stop both fail closed, so no invalid ordering resets or drops
// accumulated content.

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
	// Usage carries a cumulative per-message usage snapshot.
	Usage *UsageStats
	// Message and FinishReason carry the assembled assistant message.
	Message      *Message
	FinishReason string
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
			return d.decodeAssistantEcho(ev.Raw, false)
		case claudeSubPhaseStopped:
			return d.decodeAssistantEcho(ev.Raw, d.messagePending)
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

// claudeSubStreamMessage is the message object of a message_start event. Only
// its usage is read; assistant content arrives as separate stream events.
type claudeSubStreamMessage struct {
	Usage *claudeSubUsage `json:"usage"`
}

// claudeSubStreamEvent is the inner Anthropic Messages SSE event carried in a
// stream_event envelope. It mirrors anthropicStreamEvent but keeps the block
// index and usage components optional, so a missing value can be told apart
// from an explicit zero.
type claudeSubStreamEvent struct {
	Type         string                  `json:"type"`
	Index        *int                    `json:"index"`
	Message      *claudeSubStreamMessage `json:"message"`
	ContentBlock *anthropicContentBlock  `json:"content_block"`
	Delta        *anthropicStreamDelta   `json:"delta"`
	Usage        *claudeSubUsage         `json:"usage"`
}

// claudeSubStreamIndex validates a content-block event's required block index:
// it must be present and non-negative.
func claudeSubStreamIndex(eventType string, index *int) (int, error) {
	if index == nil {
		return 0, fmt.Errorf("%w: %s without index", errClaudeSubDecodeStream, eventType)
	}
	if *index < 0 {
		return 0, fmt.Errorf("%w: %s has invalid index %d", errClaudeSubDecodeStream, eventType, *index)
	}
	return *index, nil
}

// decodeStreamEvent unwraps a stream_event envelope, which carries one raw
// Anthropic Messages SSE event in its `event` field, and decodes that event.
// Recognised event types fail closed when a required structural field is
// missing; a non-empty type the decoder does not know is left for a future CLI.
func (d *claudeSubDecoder) decodeStreamEvent(raw json.RawMessage) ([]claudeSubDecoded, error) {
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode envelope: %v", errClaudeSubDecodeStream, err)
	}
	if len(envelope.Event) == 0 {
		return nil, fmt.Errorf("%w: envelope has no event payload", errClaudeSubDecodeStream)
	}
	var payload claudeSubStreamEvent
	if err := json.Unmarshal(envelope.Event, &payload); err != nil {
		return nil, fmt.Errorf("%w: decode payload: %v", errClaudeSubDecodeStream, err)
	}
	// An empty or null event object decodes to a payload with no type. That is a
	// malformed envelope, not a future event to ignore.
	if payload.Type == "" {
		return nil, fmt.Errorf("%w: event payload has no type", errClaudeSubDecodeStream)
	}
	// message_stop closes the streamed message and the terminal result closes
	// the turn, so a phase check before dispatch rejects any event that could
	// still mutate content or invoke a callback in those phases. Unknown event
	// types fall through unchanged and are ignored consistently.
	if err := d.rejectStreamEventInPhase(payload.Type); err != nil {
		return nil, err
	}

	switch payload.Type {
	case "message_start":
		if payload.Message == nil {
			return nil, fmt.Errorf("%w: message_start without message", errClaudeSubDecodeStream)
		}
		// One decoder follows one CLI turn, which carries exactly one
		// message_start. A second one would reset the in-flight message's
		// content, so fail closed rather than silently dropping it. A new
		// decoder instance handles another CLI turn.
		if d.messageStarted {
			return nil, fmt.Errorf("%w: duplicate message_start", errClaudeSubDecodeStream)
		}
		d.beginMessage()
		return d.usageEvents(payload.Message.Usage), nil
	case "content_block_start":
		index, err := claudeSubStreamIndex(payload.Type, payload.Index)
		if err != nil {
			return nil, err
		}
		if payload.ContentBlock == nil || payload.ContentBlock.Type == "" {
			return nil, fmt.Errorf("%w: content_block_start without a typed content_block", errClaudeSubDecodeStream)
		}
		return d.decodeBlockStart(index, payload.ContentBlock)
	case "content_block_delta":
		index, err := claudeSubStreamIndex(payload.Type, payload.Index)
		if err != nil {
			return nil, err
		}
		if payload.Delta == nil || payload.Delta.Type == "" {
			return nil, fmt.Errorf("%w: content_block_delta without a typed delta", errClaudeSubDecodeStream)
		}
		return d.decodeBlockDelta(index, payload.Delta)
	case "content_block_stop":
		index, err := claudeSubStreamIndex(payload.Type, payload.Index)
		if err != nil {
			return nil, err
		}
		return nil, d.stopBlock(index)
	case "message_delta":
		if payload.Delta == nil {
			return nil, fmt.Errorf("%w: message_delta without delta", errClaudeSubDecodeStream)
		}
		if payload.Delta.StopReason != "" {
			d.stopReason = normalizeAnthropicFinishReason(payload.Delta.StopReason)
		}
		return d.usageEvents(payload.Usage), nil
	case "message_stop":
		return d.finishMessage()
	default:
		// ping and any genuinely future stream event carry nothing the decoder
		// needs.
		return nil, nil
	}
}

// rejectStreamEventInPhase fails closed when a known streamed event must not be
// accepted in the decoder's current phase. The streaming phase accepts every
// event. Once message_stop has closed the streamed message, every known stream
// event is rejected, including a message_start: one decoder follows one CLI
// turn, so a new message cannot begin and a new decoder instance handles the
// next turn. After the terminal result every known event is rejected. Unknown
// events are ignored in every phase.
func (d *claudeSubDecoder) rejectStreamEventInPhase(eventType string) error {
	if !isKnownClaudeSubStreamEvent(eventType) {
		return nil
	}
	switch d.phase {
	case claudeSubPhaseTerminal:
		return fmt.Errorf("%w: %s after terminal result", errClaudeSubDecodeStream, eventType)
	case claudeSubPhaseStopped:
		if eventType == "message_stop" {
			return fmt.Errorf("%w: duplicate message_stop", errClaudeSubDecodeStream)
		}
		return fmt.Errorf("%w: %s after message_stop", errClaudeSubDecodeStream, eventType)
	default:
		return nil
	}
}

// isKnownClaudeSubStreamEvent reports whether the decoder acts on a streamed
// event type. Unknown types (ping and genuinely future events) are ignored and
// never mutate state or invoke a callback.
func isKnownClaudeSubStreamEvent(eventType string) bool {
	switch eventType {
	case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop":
		return true
	default:
		return false
	}
}

// decodeBlockStart handles the first sighting of a content block. Text and
// thinking blocks carry their initial content; a tool_use block is observed
// immediately so the caller can open the call handle before its result arrives.
// Every streamed start claims a fresh index: reusing an index for a second
// block of any type fails closed, so a text or thinking block can never give
// way to a tool use at the same index. A tool_use block also fails closed
// before it claims the index or publishes the observation when the index is
// already held by an accumulator or the id is already registered at another
// index: an echo-only tool use can occupy a free index with its own
// accumulator, and a streamed start must never overwrite or alias it.
func (d *claudeSubDecoder) decodeBlockStart(index int, block *anthropicContentBlock) ([]claudeSubDecoded, error) {
	switch block.Type {
	case "text":
		if err := d.claimStreamBlockIndex(index, block.Type); err != nil {
			return nil, err
		}
		return d.appendText(block.Text), nil
	case "thinking":
		if err := d.claimStreamBlockIndex(index, block.Type); err != nil {
			return nil, err
		}
		if block.Signature != "" {
			d.signature = block.Signature
		}
		return d.appendThinking(block.Thinking), nil
	case "tool_use":
		if block.ID == "" {
			return nil, fmt.Errorf("%w: tool_use block without id", errClaudeSubDecodeStream)
		}
		if block.Name == "" {
			return nil, fmt.Errorf("%w: tool_use block without name", errClaudeSubDecodeStream)
		}
		// Preflight every rejection before claiming the block index or
		// publishing the observation, so a rejected start leaves no partial
		// stream-index state. A streamed start must never overwrite an
		// accumulator already present at its index: an echo-only tool use can
		// hold one there under an explicit non-negative index, and a streamed
		// start that reuses that index must fail closed rather than clobber its
		// id, name or accumulated input. The id check catches a start that
		// reuses an id registered at another index.
		if _, exists := d.toolUses[index]; exists {
			return nil, fmt.Errorf("%w: tool_use block index %d already held by an existing accumulator", errClaudeSubDecodeStream, index)
		}
		if other, exists := d.claudeSubToolIDIndex(block.ID); exists {
			return nil, fmt.Errorf("%w: tool_use id %q already registered at index %d", errClaudeSubDecodeStream, block.ID, other)
		}
		// Preflight the initial input's size before claiming the block index,
		// creating the accumulator, recording order or publishing the
		// observation, so an oversized start leaves no claimed block,
		// accumulator, tool order, sawToolUse flag or callback behind. The
		// streamed input_json_delta path is bounded by addToolInput; this keeps
		// the same bound on a start's initial input.
		var initialInput string
		if len(block.Input) > 0 {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("%w: encode tool call %q arguments: %v", errClaudeSubDecodeStream, block.Name, err)
			}
			if len(raw) > claudeSubDecodeMaxToolInputBytes {
				return nil, claudeSubToolInputBoundError(block.Name)
			}
			initialInput = string(raw)
		}
		if err := d.claimStreamBlockIndex(index, block.Type); err != nil {
			return nil, err
		}
		var out []claudeSubDecoded
		if ev, ok := d.observeToolUse(block.ID, block.Name); ok {
			out = append(out, ev)
		}
		acc := d.toolAccumulator(index)
		acc.ID = block.ID
		acc.Name = block.Name
		d.toolOrder = append(d.toolOrder, index)
		d.sawToolUse = true
		if initialInput != "" {
			if err := d.addToolInput(index, initialInput); err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		// An unrecognised block type carries nothing the decoder consumes, but
		// its start still claims the index so a later content_block_stop finds
		// an owner and a reused index fails closed.
		if err := d.claimStreamBlockIndex(index, block.Type); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

// claimStreamBlockIndex records that a streamed content_block_start owns index
// for blockType. It fails closed when an earlier streamed start already claimed
// the index, regardless of block type, so a text or thinking block index can
// never be reused for a tool use (or the reverse). Every streamed start claims
// its index, including an unrecognised block type, so a block's stop finds its
// owner and a reused index fails closed.
func (d *claudeSubDecoder) claimStreamBlockIndex(index int, blockType string) error {
	if owner, exists := d.blocks[index]; exists {
		return fmt.Errorf("%w: content_block_start reuses index %d already owned by a %s block", errClaudeSubDecodeStream, index, owner)
	}
	if d.blocks == nil {
		d.blocks = map[int]string{}
	}
	d.blocks[index] = blockType
	return nil
}

// requireOpenBlock fails closed unless index was claimed by a streamed
// content_block_start of blockType and has not been stopped. It ties each known
// delta type to its canonical block type: text_delta to a text block,
// thinking_delta and signature_delta to a thinking block, and input_json_delta
// to a tool_use block. A delta for an unclaimed index, for a block of another
// type, or for an already stopped block is rejected. Unknown delta types carry
// nothing the decoder consumes and are ignored elsewhere. A zero-length delta
// on a correctly owned open block is still accepted.
func (d *claudeSubDecoder) requireOpenBlock(index int, blockType, deltaType string) error {
	owner, claimed := d.blocks[index]
	if !claimed {
		return fmt.Errorf("%w: %s for index %d with no streamed content_block_start", errClaudeSubDecodeStream, deltaType, index)
	}
	if owner != blockType {
		return fmt.Errorf("%w: %s for index %d owned by a %s block", errClaudeSubDecodeStream, deltaType, index, owner)
	}
	if d.stopped[index] {
		return fmt.Errorf("%w: %s for index %d after content_block_stop", errClaudeSubDecodeStream, deltaType, index)
	}
	return nil
}

// stopBlock records that the streamed content block at index has stopped. It
// requires the index to have been claimed by a streamed content_block_start, so
// a stop for an index the stream never opened fails closed, and it rejects a
// repeated stop for the same index. A stopped block then rejects any later
// delta through requireOpenBlock.
func (d *claudeSubDecoder) stopBlock(index int) error {
	if _, claimed := d.blocks[index]; !claimed {
		return fmt.Errorf("%w: content_block_stop for index %d with no streamed content_block_start", errClaudeSubDecodeStream, index)
	}
	if d.stopped[index] {
		return fmt.Errorf("%w: duplicate content_block_stop for index %d", errClaudeSubDecodeStream, index)
	}
	if d.stopped == nil {
		d.stopped = map[int]bool{}
	}
	d.stopped[index] = true
	return nil
}

// decodeBlockDelta handles one incremental content block delta. Each known
// delta type must name a claimed, still-open block of its canonical type, so a
// delta cannot fabricate content on an unclaimed index, cross into a block of
// another type, or follow a content_block_stop.
func (d *claudeSubDecoder) decodeBlockDelta(index int, delta *anthropicStreamDelta) ([]claudeSubDecoded, error) {
	switch delta.Type {
	case "text_delta":
		if err := d.requireOpenBlock(index, "text", delta.Type); err != nil {
			return nil, err
		}
		return d.appendText(delta.Text), nil
	case "thinking_delta":
		if err := d.requireOpenBlock(index, "thinking", delta.Type); err != nil {
			return nil, err
		}
		if delta.Signature != "" {
			d.signature = delta.Signature
		}
		return d.appendThinking(delta.Thinking), nil
	case "signature_delta":
		if err := d.requireOpenBlock(index, "thinking", delta.Type); err != nil {
			return nil, err
		}
		if delta.Signature != "" {
			d.signature = delta.Signature
		}
		return nil, nil
	case "input_json_delta":
		if err := d.requireOpenBlock(index, "tool_use", delta.Type); err != nil {
			return nil, err
		}
		if err := d.addToolInput(index, delta.PartialJSON); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, nil
	}
}

// claudeSubToolIDIndex returns the content-block index of the accumulator that
// holds id, and whether any block in the current message registered it.
func (d *claudeSubDecoder) claudeSubToolIDIndex(id string) (int, bool) {
	for index, acc := range d.toolUses {
		if acc.ID == id {
			return index, true
		}
	}
	return 0, false
}

// claudeSubEchoBlock is one content block of an assistant echo. Index is
// optional: the CLI's final message does not publish a content-block index, but
// when one is present the decoder checks it so an echo-only tool use cannot
// claim an index the stream already owns.
type claudeSubEchoBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Index *int            `json:"index"`
}

// decodeAssistantEcho reads a per-content-block `assistant` echo. Its text and
// usage duplicate the streamed events, so they are ignored. A tool_use the
// stream already announced is confirmed: the echo must translate to the same
// steiner name, and its accumulator and input are never touched. A tool_use the
// stream did not announce is registered only when allowEchoOnly is true, with an
// accumulator built from the echo, so the existing post-stop fallback carries
// its translated call. An entry with no id or name cannot be registered and is
// skipped.
//
// The echo is processed transactionally: every tool_use block is parsed,
// translated and validated against a staged view of the decoder state first,
// and only then are the canonical accumulators, tool order, echo state and
// callbacks committed. A repeated id in a later per-block echo is idempotent; a
// conflicting name, an incompatible index claim or an oversized input fails
// closed before any of that, so a decode call never leaves a partial echo,
// invokes a hook, or emits a final message and then fails.
func (d *claudeSubDecoder) decodeAssistantEcho(raw json.RawMessage, allowEchoOnly bool) ([]claudeSubDecoded, error) {
	var envelope struct {
		Message *struct {
			Content []claudeSubEchoBlock `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Message == nil {
		return nil, nil
	}
	// Stage the echo's new tool uses and validate every block against the
	// current, uncommitted state, so a later block's error leaves no
	// accumulator, tool order, echo state or callback from an earlier block.
	type pendingEchoToolUse struct {
		id      string
		cliName string
		key     int
		input   string
	}
	stagedUnparsed := make(map[int]string)
	var pending []pendingEchoToolUse
	stagedIDs := make(map[string]struct{})
	stagedKeys := make(map[int]struct{})
	nextEchoIndex := d.echoToolIndex
	for _, block := range envelope.Message.Content {
		if block.Type != "tool_use" || block.ID == "" || block.Name == "" {
			continue
		}
		name := d.toolName(block.Name)
		if _, dup := stagedIDs[block.ID]; dup {
			return nil, fmt.Errorf("%w: assistant echo repeats tool_use id %q", errClaudeSubDecodeStream, block.ID)
		}
		stagedIDs[block.ID] = struct{}{}
		if index, known := d.claudeSubToolIDIndex(block.ID); known {
			acc := d.toolUses[index]
			if d.toolName(acc.Name) != name {
				return nil, fmt.Errorf("%w: assistant echo tool_use id %q name %q conflicts with streamed name %q", errClaudeSubDecodeStream, block.ID, name, d.toolName(acc.Name))
			}
			if block.Index != nil && *block.Index != index {
				return nil, fmt.Errorf("%w: assistant echo tool_use id %q claims index %d but streamed index %d", errClaudeSubDecodeStream, block.ID, *block.Index, index)
			}
			if raw, ok, err := claudeSubValidatedUnparsedToolInput(block.Input, acc.Input.String()); err != nil {
				return nil, err
			} else if ok {
				stagedUnparsed[index] = raw
			}
			continue
		}
		if !allowEchoOnly {
			return nil, fmt.Errorf("%w: assistant echo tool_use id %q was not announced by the stream", errClaudeSubDecodeStream, block.ID)
		}
		if d.echoProcessed {
			return nil, fmt.Errorf("%w: assistant echo introduces tool_use id %q after fallback echo", errClaudeSubDecodeStream, block.ID)
		}
		key, err := d.echoToolKey(block.Index, stagedKeys, &nextEchoIndex)
		if err != nil {
			return nil, err
		}
		stagedKeys[key] = struct{}{}
		var input string
		if len(block.Input) > 0 && string(block.Input) != "null" {
			var object map[string]any
			if err := json.Unmarshal(block.Input, &object); err != nil {
				return nil, fmt.Errorf("%w: decode assistant echo tool call %q arguments: %v", errClaudeSubDecodeStream, block.Name, err)
			}
			encoded, err := json.Marshal(object)
			if err != nil {
				return nil, fmt.Errorf("%w: encode assistant echo tool call %q arguments: %v", errClaudeSubDecodeStream, block.Name, err)
			}
			if len(encoded) > claudeSubDecodeMaxToolInputBytes {
				return nil, claudeSubToolInputBoundError(block.Name)
			}
			input = string(encoded)
		}
		pending = append(pending, pendingEchoToolUse{id: block.ID, cliName: block.Name, key: key, input: input})
	}
	// Every block validated: commit the staged echo and invoke the observation
	// callbacks. A validation failure above returns before this point.
	d.echoProcessed = true
	if d.phase == claudeSubPhaseStreaming {
		d.streamEchoProcessed = true
	}
	d.echoToolIndex = nextEchoIndex
	for index, raw := range stagedUnparsed {
		if d.unparsedToolInputs == nil {
			d.unparsedToolInputs = make(map[int]string)
		}
		d.unparsedToolInputs[index] = raw
	}
	var out []claudeSubDecoded
	for _, p := range pending {
		if ev, ok := d.observeToolUse(p.id, p.cliName); ok {
			out = append(out, ev)
		}
		acc := d.toolAccumulator(p.key)
		acc.ID = p.id
		acc.Name = p.cliName
		d.toolOrder = append(d.toolOrder, p.key)
		d.sawToolUse = true
		if p.input != "" {
			if err := d.seedEchoToolInput(p.key, p.input); err != nil {
				return nil, err
			}
		}
	}
	// The echo has been fully committed, so any fallback tool use it introduced
	// is now in the canonical accumulator. Emit the message the stream stopped
	// on, assembled from that state, before returning the echo's observations.
	msg, err := d.flushPendingMessage()
	if err != nil {
		return nil, err
	}
	if msg != nil {
		out = append(out, *msg)
	}
	return out, nil
}

// echoToolKey returns the accumulator key for a tool use the assistant echo
// introduces, staged against the current, uncommitted state so the echo is
// validated as a whole before anything is committed. When the echo exposes a
// content-block index, that index is reused only after checking it is
// non-negative and owned by neither a streamed block (of any type), another
// accumulator, nor an earlier entry of the same echo (tracked in staged). The
// key it returns is only a container: it conveys no stream authority, so a
// later input_json_delta still fails unless a streamed tool_use block claimed
// the index. Otherwise a strictly negative key is allocated by decrementing
// next, which the caller commits only after the whole echo validates. Streamed
// content-block indices are validated non-negative, so a negative key cannot
// collide with a streamed block or a second echo-only entry.
func (d *claudeSubDecoder) echoToolKey(index *int, staged map[int]struct{}, next *int) (int, error) {
	if index == nil {
		*next--
		return *next, nil
	}
	if *index < 0 {
		return 0, fmt.Errorf("%w: assistant echo tool_use has invalid index %d", errClaudeSubDecodeStream, *index)
	}
	if owner, exists := d.blocks[*index]; exists {
		return 0, fmt.Errorf("%w: assistant echo tool_use claims index %d already owned by a streamed %s block", errClaudeSubDecodeStream, *index, owner)
	}
	if _, exists := d.toolUses[*index]; exists {
		return 0, fmt.Errorf("%w: assistant echo tool_use claims index %d already in use", errClaudeSubDecodeStream, *index)
	}
	if _, exists := staged[*index]; exists {
		return 0, fmt.Errorf("%w: assistant echo tool_use claims index %d already in use", errClaudeSubDecodeStream, *index)
	}
	return *index, nil
}

// decodeResult decodes one terminal result envelope. A malformed envelope, a
// subtype that is neither "success" nor an "error_*" subtype, or a "success"
// subtype marked is_error is a fail-closed error. A recognised error result is
// surfaced as an event.
func (d *claudeSubDecoder) decodeResult(raw json.RawMessage) ([]claudeSubDecoded, error) {
	var payload struct {
		Subtype           string            `json:"subtype"`
		IsError           bool              `json:"is_error"`
		Result            string            `json:"result"`
		NumTurns          int               `json:"num_turns"`
		StopReason        string            `json:"stop_reason"`
		TotalCostUSD      float64           `json:"total_cost_usd"`
		Usage             *claudeSubUsage   `json:"usage"`
		PermissionDenials []json.RawMessage `json:"permission_denials"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", errClaudeSubResultEnvelope, err)
	}
	isError, err := claudeSubResultIsError(payload.Subtype, payload.IsError)
	if err != nil {
		return nil, err
	}
	// A success result is only valid once message_stop has closed the streamed
	// message. Accepting it earlier would close the turn while content is still
	// streaming and strand the final assistant message, so fail closed without
	// flushing, advancing the phase or emitting the result. A recognised error
	// result is surfaced before message_stop too: the CLI can fail before any
	// message completes, and its subtype and text are the real cause. No
	// message is emitted for a message_stop that never arrived.
	if d.phase != claudeSubPhaseStopped && !isError {
		return nil, fmt.Errorf("%w: result before message_stop", errClaudeSubDecodeStream)
	}
	// A stopped message is flushed before the terminal result so a turn whose
	// stream never produced an assistant echo still hands its final assistant
	// output downstream, and a recognised error result still flushes it. A
	// malformed or unsupported result fails closed above, before any flush, so a
	// misleading final message is never emitted after the error.
	var out []claudeSubDecoded
	msg, err := d.flushPendingMessage()
	if err != nil {
		return nil, err
	}
	if msg != nil {
		out = append(out, *msg)
	}
	// The validated result closes the turn: nothing may mutate state or invoke a
	// hook after it.
	d.phase = claudeSubPhaseTerminal
	return append(out, claudeSubDecoded{
		Kind: claudeSubDecodeResult,
		Result: &claudeSubResult{
			Subtype:           payload.Subtype,
			IsError:           isError,
			Text:              payload.Result,
			NumTurns:          payload.NumTurns,
			StopReason:        payload.StopReason,
			TotalCostUSD:      payload.TotalCostUSD,
			PermissionDenials: len(payload.PermissionDenials),
			Usage:             payload.Usage.toUsageStats(),
		},
	}), nil
}

// claudeSubResultIsError classifies a result subtype. "success" is a clean
// turn; an "error_*" subtype is a terminal error; anything else (including an
// empty subtype) is unsupported and fails closed.
func claudeSubResultIsError(subtype string, isError bool) (bool, error) {
	switch {
	case subtype == "success":
		if isError {
			return false, fmt.Errorf("%w: success subtype marked is_error", errClaudeSubResultEnvelope)
		}
		return false, nil
	case strings.HasPrefix(subtype, "error_"):
		return true, nil
	default:
		return false, fmt.Errorf("%w: unsupported subtype %q", errClaudeSubResultEnvelope, truncateRunes(subtype, claudeSubDecodeMaxExcerptRunes))
	}
}

// beginMessage resets per-message accumulation at message_start.
func (d *claudeSubDecoder) beginMessage() {
	d.content.Reset()
	d.thinking.Reset()
	d.signature = ""
	d.sawContent = false
	d.sawThinking = false
	d.toolUses = nil
	d.blocks = nil
	d.echoToolIndex = 0
	d.sawToolUse = false
	d.stopReason = ""
	d.usage = nil
	d.observed = map[string]struct{}{}
	d.toolOrder = nil
	d.unparsedToolInputs = nil
	d.phase = claudeSubPhaseStreaming
	d.messagePending = false
	d.echoProcessed = false
	d.streamEchoProcessed = false
	d.messageStarted = true
	d.stopped = nil
}

// appendText accumulates and emits one text delta.
func (d *claudeSubDecoder) appendText(text string) []claudeSubDecoded {
	if text == "" {
		return nil
	}
	d.content.WriteString(text)
	d.sawContent = true
	return []claudeSubDecoded{{Kind: claudeSubDecodeText, Text: text}}
}

// appendThinking accumulates and emits one thinking delta.
func (d *claudeSubDecoder) appendThinking(thinking string) []claudeSubDecoded {
	if thinking == "" {
		return nil
	}
	d.thinking.WriteString(thinking)
	d.sawThinking = true
	return []claudeSubDecoded{{Kind: claudeSubDecodeThinking, Thinking: thinking}}
}

// usageEvents merges a usage snapshot into the current message's cumulative
// usage and surfaces the running total. Each component is merged only when the
// snapshot reports it, so a later snapshot that omits an input or cache
// component retains the earlier value instead of clearing it: the CLI splits a
// message's usage across message_start and message_delta and omits what it has
// not (re)reported. Each event is the cumulative value for the message, not a
// delta, and is a fresh conversion so a caller that keeps an event is not
// affected by a later merge.
func (d *claudeSubDecoder) usageEvents(u *claudeSubUsage) []claudeSubDecoded {
	if u == nil {
		return nil
	}
	if d.usage == nil {
		d.usage = &claudeSubUsage{}
	}
	if u.InputTokens != nil {
		d.usage.InputTokens = u.InputTokens
	}
	if u.CacheCreationInputTokens != nil {
		d.usage.CacheCreationInputTokens = u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens != nil {
		d.usage.CacheReadInputTokens = u.CacheReadInputTokens
	}
	if u.OutputTokens != nil {
		d.usage.OutputTokens = u.OutputTokens
	}
	return []claudeSubDecoded{{Kind: claudeSubDecodeUsage, Usage: d.usage.toUsageStats()}}
}

// observeToolUse reports a tool-use id once per message, translating its name
// and invoking the observation hook. It returns false when the id or name is
// empty or the id was already observed; the streamed block start rejects those
// cases outright, and this keeps the assistant echo from observing a nameless
// call.
func (d *claudeSubDecoder) observeToolUse(id, cliName string) (claudeSubDecoded, bool) {
	if id == "" || cliName == "" {
		return claudeSubDecoded{}, false
	}
	if _, seen := d.observed[id]; seen {
		return claudeSubDecoded{}, false
	}
	d.observed[id] = struct{}{}
	name := d.toolName(cliName)
	if d.hooks.ObserveToolUse != nil {
		d.hooks.ObserveToolUse(id, name)
	}
	return claudeSubDecoded{Kind: claudeSubDecodeToolUse, ToolUseID: id, ToolName: name}, true
}

// toolAccumulator returns the argument accumulator for a content block index,
// creating it on first use.
func (d *claudeSubDecoder) toolAccumulator(index int) *anthropicToolUseAccumulator {
	if d.toolUses == nil {
		d.toolUses = map[int]*anthropicToolUseAccumulator{}
	}
	acc := d.toolUses[index]
	if acc == nil {
		acc = &anthropicToolUseAccumulator{}
		d.toolUses[index] = acc
	}
	return acc
}

// addToolInput appends streamed argument JSON to the accumulator opened by a
// validated tool_use block start. It requires the index to be owned by a
// streamed tool_use block: a matching accumulator id and name is not enough,
// because an echo-only tool use can hold an accumulator at a free index without
// any streamed block start having claimed it. It never creates an accumulator,
// so an input_json_delta with no matching tool_use block, or one whose
// accumulator lacks a valid id and name, fails closed instead of fabricating a
// nameless call. The accumulated arguments stay bounded.
func (d *claudeSubDecoder) addToolInput(index int, partial string) error {
	if d.blocks[index] != "tool_use" {
		return fmt.Errorf("%w: input_json_delta for index %d with no streamed tool_use block start", errClaudeSubDecodeStream, index)
	}
	acc := d.toolUses[index]
	if acc == nil || acc.ID == "" || acc.Name == "" {
		return fmt.Errorf("%w: input_json_delta for an unobserved tool_use block at index %d", errClaudeSubDecodeStream, index)
	}
	if err := appendBoundedToolInput(acc, partial); err != nil {
		return err
	}
	d.sawToolUse = true
	return nil
}

// seedEchoToolInput records the assistant echo's complete argument object on an
// echo-only tool-use accumulator. It is deliberately separate from addToolInput:
// an echo is never stream authority, so seeding it neither requires nor grants a
// streamed block index. The accumulated arguments stay bounded.
func (d *claudeSubDecoder) seedEchoToolInput(key int, partial string) error {
	acc := d.toolUses[key]
	if acc == nil || acc.ID == "" || acc.Name == "" {
		return fmt.Errorf("%w: assistant echo tool_use has no accumulator at key %d", errClaudeSubDecodeStream, key)
	}
	return appendBoundedToolInput(acc, partial)
}

// claudeSubToolInputBoundError is the fail-closed error for a tool call whose
// accumulated argument JSON would exceed the input bound. It is shared by the
// streamed delta, echo-seeding and start-preflight paths so all three report
// the same bound.
func claudeSubToolInputBoundError(name string) error {
	return fmt.Errorf("%w: tool call %q argument JSON exceeds %d bytes", errClaudeSubDecodeStream, name, claudeSubDecodeMaxToolInputBytes)
}

// appendBoundedToolInput appends one tool-argument fragment, failing closed when
// the accumulated JSON would exceed the input bound. It is shared by the
// streamed delta path and the echo-seeding path so both stay bounded.
func appendBoundedToolInput(acc *anthropicToolUseAccumulator, partial string) error {
	if acc.Input.Len()+len(partial) > claudeSubDecodeMaxToolInputBytes {
		return claudeSubToolInputBoundError(acc.Name)
	}
	acc.Input.WriteString(partial)
	return nil
}

// finishMessage records that the current message's stream has stopped. A
// per-content-block echo may already have been processed while streaming, in
// which case the assembled message is emitted at message_stop. Otherwise it is
// held for a post-stop fallback echo or the terminal result. The phase guard
// rejects a second stop before this point.
func (d *claudeSubDecoder) finishMessage() ([]claudeSubDecoded, error) {
	d.phase = claudeSubPhaseStopped
	d.messagePending = true
	if !d.streamEchoProcessed {
		return nil, nil
	}
	msg, err := d.flushPendingMessage()
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, nil
	}
	return []claudeSubDecoded{*msg}, nil
}

// flushPendingMessage emits the held final assistant message for the stopped
// message, if one is pending, and clears the pending flag so it is emitted
// exactly once. It assembles from current state, so an assistant echo's fallback
// tool uses are already in the canonical accumulator. It returns nil when no
// message is pending. The pending flag is cleared only after assembly succeeds,
// so a failed assembly (for example a malformed accumulated tool input) leaves
// the message pending and recoverable instead of silently discarding it.
func (d *claudeSubDecoder) flushPendingMessage() (*claudeSubDecoded, error) {
	if !d.messagePending {
		return nil, nil
	}
	msg, err := d.assembleMessage()
	if err != nil {
		return nil, err
	}
	d.messagePending = false
	return msg, nil
}

// assembleMessage builds the final assistant message for the current message
// from its accumulated state, translating each tool-call name. It is called once
// per message: at message_stop after an in-stream echo, at a post-stop fallback
// echo, or at the terminal result otherwise.
func (d *claudeSubDecoder) assembleMessage() (*claudeSubDecoded, error) {
	message := Message{Role: MessageRoleAssistant}
	if d.sawContent {
		message.Content = d.content.String()
	}
	if d.sawThinking {
		message.ReasoningContent = d.thinking.String()
		if d.signature != "" {
			message.ProviderMetadata = &MessageProviderMetadata{
				Anthropic: &AnthropicMessageMetadata{ThinkingSignature: d.signature},
			}
		}
	}
	if d.sawToolUse {
		calls, err := d.finalizeOrderedToolUses()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errClaudeSubDecodeStream, err)
		}
		for i := range calls {
			calls[i].Name = d.toolName(calls[i].Name)
		}
		message.ToolCalls = calls
	}
	return &claudeSubDecoded{
		Kind:         claudeSubDecodeMessage,
		Message:      &message,
		FinishReason: d.stopReason,
	}, nil
}

// finalizeOrderedToolUses finalizes the current message's tool-use accumulators
// in their canonical observation order. Re-keying the accumulators by ordinal
// position lets finalizeAnthropicToolUses apply its argument validation in the
// order the tools were observed; iterating the map's own keys would sort the
// negative synthetic keys an echo-only fallback uses ahead of the streamed
// calls that preceded them. Every key appears once, so no call is duplicated or
// dropped.
func (d *claudeSubDecoder) finalizeOrderedToolUses() ([]ToolCall, error) {
	calls := make([]ToolCall, 0, len(d.toolOrder))
	for _, key := range d.toolOrder {
		acc := d.toolUses[key]
		if raw, ok := d.unparsedToolInputs[key]; ok {
			calls = append(calls, ToolCall{
				ID:           acc.ID,
				Name:         acc.Name,
				RawArguments: raw,
				Arguments: map[string]any{
					claudeSubUnparsedToolInputKey: map[string]any{
						"raw": raw,
						"len": len([]byte(raw)),
					},
				},
			})
			continue
		}
		ordinary, err := finalizeAnthropicToolUses(map[int]*anthropicToolUseAccumulator{0: acc})
		if err != nil {
			return nil, err
		}
		calls = append(calls, ordinary...)
	}
	return calls, nil
}

// toolName translates a CLI-published tool name through the hook, defaulting to
// the name unchanged when no hook is set.
const claudeSubUnparsedToolInputKey = "__unparsedToolInput"

// claudeSubValidatedUnparsedToolInput accepts the CLI-certified sentinel only
// for an assistant echo that confirms a streamed tool use. Its shape, raw bytes
// and byte length must all match the accumulated input exactly.
func claudeSubValidatedUnparsedToolInput(input json.RawMessage, accumulated string) (string, bool, error) {
	if len(input) == 0 || string(input) == "null" {
		return "", false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil {
		return "", false, fmt.Errorf("%w: decode unparsed tool input sentinel: %v", errClaudeSubDecodeStream, err)
	}
	field, present := object[claudeSubUnparsedToolInputKey]
	if !present {
		return "", false, nil
	}
	if len(object) != 1 {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel has extra fields", errClaudeSubDecodeStream)
	}
	var sentinel map[string]json.RawMessage
	if err := json.Unmarshal(field, &sentinel); err != nil || len(sentinel) != 2 {
		return "", false, fmt.Errorf("%w: malformed unparsed tool input sentinel", errClaudeSubDecodeStream)
	}
	var raw string
	var length int
	if rawField, ok := sentinel["raw"]; !ok || json.Unmarshal(rawField, &raw) != nil {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel raw field is invalid", errClaudeSubDecodeStream)
	}
	if lenField, ok := sentinel["len"]; !ok || json.Unmarshal(lenField, &length) != nil || length != len([]byte(raw)) {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel length is invalid", errClaudeSubDecodeStream)
	}
	if raw != accumulated {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel does not match accumulated input", errClaudeSubDecodeStream)
	}
	if len([]byte(raw)) > claudeSubDecodeMaxToolInputBytes {
		return "", false, claudeSubToolInputBoundError("unparsed")
	}
	return raw, true, nil
}

func (d *claudeSubDecoder) toolName(cliName string) string {
	if d.hooks.ToolName == nil {
		return cliName
	}
	return d.hooks.ToolName(cliName)
}
