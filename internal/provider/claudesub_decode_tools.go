package provider

import (
	"encoding/json"
	"fmt"
	"unicode/utf16"
)

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

// finalizeOrderedToolUses finalizes the current message's tool-use accumulators
// in their canonical observation order. Re-keying the accumulators by ordinal
// position lets finalizeAnthropicToolUses apply its argument validation in the
// order the tools were observed; iterating the map's own keys would sort the
// negative synthetic keys an echo-only fallback uses ahead of the streamed
// calls that preceded them. Every key appears once, so no call is duplicated or
// dropped.
func (d *claudeSubDecoder) finalizeOrderedToolUses() []ToolCall {
	calls := make([]ToolCall, 0, len(d.toolOrder))
	for _, key := range d.toolOrder {
		acc := d.toolUses[key]
		if raw, ok := d.unparsedToolInputs[key]; ok {
			calls = append(calls, ToolCall{
				ID:           acc.ID,
				Name:         acc.Name,
				RawArguments: raw,
				Arguments: map[string]any{
					UnparsedToolInputKey: map[string]any{
						"raw": raw,
						"len": len([]byte(raw)),
					},
				},
			})
			continue
		}
		calls = append(calls, finalizeAnthropicToolUses(map[int]*anthropicToolUseAccumulator{0: acc})...)
	}
	return calls
}

// claudeSubUnparsedRawMaxUnits is the CLI's cap on the raw prefix of an
// unparsed tool input, in UTF-16 code units.
const claudeSubUnparsedRawMaxUnits = 2048

// claudeSubUnparsedRawPrefix reproduces the CLI's truncation of a streamed tool
// input. It returns the longest byte prefix of s that fits in
// claudeSubUnparsedRawMaxUnits UTF-16 code units, so a surrogate pair cut by
// the cap is dropped. Invalid UTF-8 bytes count as one unit each. Inputs within
// the cap are returned unchanged.
func claudeSubUnparsedRawPrefix(s string) string {
	units := 0
	for i, r := range s {
		n := utf16.RuneLen(r)
		if n < 0 {
			n = 1
		}
		if units+n > claudeSubUnparsedRawMaxUnits {
			return s[:i]
		}
		units += n
	}
	return s
}

// claudeSubValidatedUnparsedToolInput accepts the CLI-certified sentinel only
// for an assistant echo that confirms a streamed tool use. Its raw field must
// equal the streamed input, or the CLI's truncated prefix of it (see
// claudeSubUnparsedRawPrefix). The len field must be a non-negative JSON
// integer. It is the full input length in UTF-16 code units, but it is not
// compared with raw; the emitted sentinel recomputes the byte length. The
// returned string is the full accumulated input, not raw.
func claudeSubValidatedUnparsedToolInput(input json.RawMessage, accumulated string) (string, bool, error) {
	if len(input) == 0 || string(input) == "null" {
		return "", false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil {
		return "", false, fmt.Errorf("%w: decode unparsed tool input sentinel: %v", errClaudeSubDecodeStream, err)
	}
	field, present := object[UnparsedToolInputKey]
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
	if rawField, ok := sentinel["raw"]; !ok || json.Unmarshal(rawField, &raw) != nil {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel raw field is invalid", errClaudeSubDecodeStream)
	}
	// A pointer makes a JSON null fail the nil check instead of decoding as 0.
	var length *int
	if lenField, ok := sentinel["len"]; !ok || json.Unmarshal(lenField, &length) != nil || length == nil || *length < 0 {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel length is invalid", errClaudeSubDecodeStream)
	}
	if raw != accumulated && raw != claudeSubUnparsedRawPrefix(accumulated) {
		return "", false, fmt.Errorf("%w: unparsed tool input sentinel does not match accumulated input", errClaudeSubDecodeStream)
	}
	if len([]byte(accumulated)) > claudeSubDecodeMaxToolInputBytes {
		return "", false, claudeSubToolInputBoundError("unparsed")
	}
	return accumulated, true, nil
}

// toolName translates a CLI-published tool name through the hook, defaulting to
// the name unchanged when no hook is set.
func (d *claudeSubDecoder) toolName(cliName string) string {
	if d.hooks.ToolName == nil {
		return cliName
	}
	return d.hooks.ToolName(cliName)
}
