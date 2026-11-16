package provider

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"
)

func claudeSubConsume(ctx context.Context, s *claudeSubSession, emit func(ChatChunk) error) error {
	// The per-delivery facts were reset by claudeSubDeliver before it wrote the
	// delta, so they describe this delivery only.
	decoder := newClaudeSubDecoder(claudeSubDecodeHooks{
		ToolName: func(name string) string {
			if s.host == nil {
				return name
			}
			return s.host.steinerToolName(name)
		},
		ObserveToolUse: func(id, _ string) {
			s.beginPendingCall(id)
		},
	})
	var ordinary *claudeSubDecoded
	// emitFinal sends a Done chunk. Only a parent call hands back backend state,
	// as of the messages committed so far; advisor and sub-agent calls return
	// none (D4).
	emitFinal := func(chunk ChatChunk) error {
		if s.parentCall {
			state, err := s.sync.backendState(s.key)
			if err != nil {
				return err
			}
			chunk.BackendState = state
		}
		return emit(chunk)
	}
	for {
		if err := s.overage(); err != nil {
			return err
		}
		ev, ok, interrupted := s.queue.popInterruptible(ctx, s.overageAbort)
		if interrupted {
			return s.overage()
		}
		if !ok {
			if err := s.overage(); err != nil {
				return err
			}
			if err := s.conn.Err(); err != nil {
				return err
			}
			return errors.New("claude CLI connection closed")
		}
		if err := s.overage(); err != nil {
			return err
		}
		if limit := claudeSubEventUsageLimit(ev); limit != nil {
			// The limit stops this delivery, but the CLI may still emit the rest of
			// the turn, so the process cannot serve a later call.
			s.abandon()
			return limit
		}
		if ev.SessionID != "" {
			s.sync.cliSessionID = ev.SessionID
		}
		decoded, err := decoder.decode(ev)
		if err != nil {
			return err
		}
		for _, item := range decoded {
			switch item.Kind {
			case claudeSubDecodeText:
				if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: item.Text}}); err != nil {
					return err
				}
			case claudeSubDecodeThinking:
				if err := emit(ChatChunk{Thinking: item.Thinking}); err != nil {
					return err
				}
			case claudeSubDecodeUsage:
				if err := emit(ChatChunk{Usage: item.Usage}); err != nil {
					return err
				}
			case claudeSubDecodeAPIRetry:
				// The CLI retried its own API request before producing output, so
				// nothing was discarded and the chunk carries no content.
				if item.Retry == nil {
					continue
				}
				if err := emit(claudeSubAPIRetryChunk(*item.Retry)); err != nil {
					return err
				}
			case claudeSubDecodeToolUse:
				// The observation hook registers the opaque MCP handle before the
				// later assistant message exposes the complete call.
			case claudeSubDecodeMessage:
				if len(item.Message.ToolCalls) > 0 {
					s.sync.commitAssistant(*item.Message, item.UUID)
					s.delivery.assistant = true
					if s.host != nil {
						s.host.settle()
					}
					if err := emitFinal(ChatChunk{Delta: *item.Message, ContentSnapshot: true, Done: true, FinishReason: item.FinishReason, Usage: item.Usage}); err != nil {
						return err
					}
					return nil
				}
				copy := item
				ordinary = &copy
			case claudeSubDecodeResult:
				s.delivery.terminal = true
				if s.host != nil {
					// The turn has no tool calls left to wait for.
					s.host.settle()
				}
				if item.Result.IsError {
					return claudeSubTurnFailure(item.Result)
				}
				// The result's usage is cumulative over the whole CLI query and is not
				// context occupancy. The final chunk reports the terminal assistant
				// message's own usage, or none when that message has no usage.
				if ordinary != nil {
					s.sync.commitAssistant(*ordinary.Message, ordinary.UUID)
					s.delivery.assistant = true
					if err := emitFinal(ChatChunk{Delta: *ordinary.Message, ContentSnapshot: true, Done: true, FinishReason: normalizeAnthropicFinishReason(item.Result.StopReason), Usage: ordinary.Usage}); err != nil {
						return err
					}
				} else if err := emitFinal(ChatChunk{Done: true, FinishReason: normalizeAnthropicFinishReason(item.Result.StopReason)}); err != nil {
					return err
				}
				return nil
			}
		}
	}
}

// claudeSubAPIRetryChunk reports the CLI's own API retry as a warning the user
// sees. It carries no assistant content and is not a stream reset: the CLI
// retries before it produces output, so nothing already delivered is discarded.
func claudeSubAPIRetryChunk(retry claudeSubAPIRetry) ChatChunk {
	return ChatChunk{
		Diagnostic:       claudeSubAPIRetryMessage(retry),
		Severity:         "warning",
		RetryAttempt:     retry.Attempt,
		RetryMaxAttempts: retry.MaxRetries,
		RetryDelay:       retry.RetryDelay,
		cliRetry:         &retry,
	}
}

// claudeSubAPIRetryNotice reports a chunk that only reports the CLI's own API
// retry. It produced no output, so it must not close the pre-output retry
// window and it must not trigger the pending replacement warning.
func claudeSubAPIRetryNotice(chunk ChatChunk) bool {
	return chunk.cliRetry != nil
}

// claudeSubAPIRetrySummary reports the CLI's own API retries of one folded call
// as a single warning. A non-streaming call has no activity row to show them on.
func claudeSubAPIRetrySummary(count int, last *claudeSubAPIRetry) string {
	category := last.Error
	if category == "" {
		category = "unknown"
	}
	summary := fmt.Sprintf("claude CLI retried the API request %d time(s) (last: %s", count, category)
	if last.ErrorStatus != 0 {
		summary += fmt.Sprintf(", status %d", last.ErrorStatus)
	}
	return summary + ")"
}

// claudeSubAPIRetryMessage describes one CLI API retry. It never claims content
// was discarded, because the CLI retries before it produces any.
func claudeSubAPIRetryMessage(retry claudeSubAPIRetry) string {
	category := retry.Error
	if category == "" {
		category = "unknown"
	}
	message := "claude CLI retrying API request after " + category
	if retry.ErrorStatus != 0 {
		message += fmt.Sprintf(" (status %d)", retry.ErrorStatus)
	}
	// The CLI does not always report a limit, so an unknown one is left out
	// rather than shown as zero.
	attempt := fmt.Sprintf("attempt %d", retry.Attempt)
	if retry.MaxRetries > 0 {
		attempt = fmt.Sprintf("attempt %d/%d", retry.Attempt, retry.MaxRetries)
	}
	return fmt.Sprintf("%s, %s, next try in %s", message, attempt, retry.RetryDelay)
}

// claudeSubFailureTextMax bounds the result text quoted in a turn failure.
const claudeSubFailureTextMax = 300

// claudeSubTurnFailure reports a failed turn, quoting the result text when the
// CLI supplied one. Long text is cut at a rune boundary and marked with "...".
func claudeSubTurnFailure(r *claudeSubResult) error {
	text := r.Text
	if len(text) > claudeSubFailureTextMax {
		cut := claudeSubFailureTextMax
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "..."
	}
	if text == "" {
		return fmt.Errorf("claude_subscription turn failed: %s", r.Subtype)
	}
	return &claudeSubDetailError{
		detail: fmt.Errorf("claude_subscription turn failed: %s: %s", r.Subtype, text),
		safe:   fmt.Errorf("claude_subscription turn failed: %s", r.Subtype),
	}
}
