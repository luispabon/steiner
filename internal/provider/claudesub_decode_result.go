package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
	d.messageUUID = ""
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
