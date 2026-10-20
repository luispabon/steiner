package provider

import (
	"encoding/json"
	"fmt"
)

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
func (d *claudeSubDecoder) decodeAssistantEcho(raw json.RawMessage, uuid string, allowEchoOnly bool) ([]claudeSubDecoded, error) {
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
	// An echo that arrives after the message was emitted belongs to no emitted
	// message, so it must not replace the message's final uuid.
	if d.phase == claudeSubPhaseStreaming || d.messagePending {
		d.messageUUID = uuid
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
