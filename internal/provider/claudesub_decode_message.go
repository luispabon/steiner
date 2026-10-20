package provider

import "fmt"

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
		Usage:        d.usage.toUsageStats(),
		UUID:         d.messageUUID,
	}, nil
}
