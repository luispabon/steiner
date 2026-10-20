package provider

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"
)

func claudeSubConsume(ctx context.Context, s *claudeSubSession, emit func(ChatChunk) error) error {
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
			case claudeSubDecodeToolUse:
				// The observation hook registers the opaque MCP handle before the
				// later assistant message exposes the complete call.
			case claudeSubDecodeMessage:
				if len(item.Message.ToolCalls) > 0 {
					s.sync.commitAssistant(*item.Message, item.UUID)
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
