package provider

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

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
		switch eventType {
		case "message_stop":
			return fmt.Errorf("%w: duplicate message_stop", errClaudeSubDecodeStream)
		case "message_start":
			// The CLI can finish one response and start another within the same
			// turn, typically after its safety safeguards refused the request. The second response is
			// never accepted, so the caller gets an explanation and the affected
			// response stops.
			return fmt.Errorf("%w: Claude CLI started another response within the turn after the previous response ended, possibly after its safety safeguards refused the request; the response was stopped", errClaudeSubDecodeStream)
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

// stopBlock records that the streamed content block at index has stopped. A
// stop for an index no streamed content_block_start claimed has no block to
// finish, so it is ignored with a debug record and does not claim the index. It
// rejects a repeated stop for the same index. A stopped block then rejects any
// later delta through requireOpenBlock.
func (d *claudeSubDecoder) stopBlock(index int) error {
	if _, claimed := d.blocks[index]; !claimed {
		slog.Debug("claude_subscription content_block_stop without streamed content_block_start ignored", "index", index)
		return nil
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
