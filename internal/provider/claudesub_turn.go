package provider

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"
)

func claudeSubSessionKey(req ChatRequest) string {
	key := req.TransportSession
	if key == "" {
		key = req.ParentTransportSession
	}
	if key == "" {
		key = "default"
	}
	if req.AdvisorCacheProfile {
		key = claudeSubAdvisorSessionKey(key)
	}
	return key
}

func claudeSubEffortFor(req ChatRequest) string {
	if req.Reasoning == nil {
		return ""
	}
	return req.Reasoning.Effort
}

func claudeSubTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, emit func(ChatChunk) error) error {
	key := claudeSubSessionKey(req)
	spec := claudeSubStartSpec{Model: req.Model, Effort: claudeSubEffortFor(req)}
	if !req.AdvisorCacheProfile {
		spec.SystemPrompt = claudeSubSystemPrompt(req.Messages)
		spec.Tools = req.Tools
	}
	s, err := pool.acquire(ctx, key, spec)
	if err != nil {
		return err
	}
	defer pool.release(s)

	if err := claudeSubFinishPending(ctx, s, req); err != nil {
		return err
	}
	var delta claudeSubDelta
	if req.AdvisorCacheProfile {
		delta, err = s.sync.planAdvisor(req)
	} else {
		delta, err = s.sync.plan(req)
	}
	if err != nil {
		return err
	}
	if len(delta.User) > 0 {
		if usage, usageErr := s.control.getUsage(ctx); usageErr != nil {
			return usageErr
		} else if err := claudeSubUsageGate(usage); err != nil {
			return err
		}
	}
	if s.model != req.Model {
		if _, err := s.control.setModel(ctx, req.Model); err != nil {
			return err
		}
		s.model = req.Model
	}
	if effort := claudeSubEffortFor(req); effort != "" && effort != s.effort {
		if _, err := s.control.setEffort(ctx, effort); err != nil {
			return err
		}
		s.effort = effort
	}
	if s.host != nil {
		if err := s.host.setTools(req.Tools); err != nil {
			return err
		}
	}
	// Check every tool result before resolving any, so an unknown result
	// changes nothing.
	for _, result := range delta.ToolResults {
		if claudeSubPendingByID(s, result.ToolCallID) == nil {
			return fmt.Errorf("claude_subscription tool result %q has no pending call", result.ToolCallID)
		}
	}
	if !req.AdvisorCacheProfile {
		s.sync.adopt(delta)
	}
	for _, result := range delta.ToolResults {
		call := claudeSubPendingByID(s, result.ToolCallID)
		if call == nil {
			return fmt.Errorf("claude_subscription tool result %q has no pending call", result.ToolCallID)
		}
		s.host.resolve(call.Handle, claudeSubToolResultFromMessage(result))
		claudeSubRemovePending(s, result.ToolCallID)
		s.sync.commitToolResult(result)
	}
	if len(delta.User) > 0 {
		// A user line starts a new CLI query.
		s.queryUsage = UsageStats{}
		if err := writeClaudeSubUser(s.conn, claudeSubUserBlocks(delta.User)); err != nil {
			return err
		}
	}
	if len(delta.ToolResults) > 0 || len(delta.User) > 0 {
		s.sync.commitSent(delta)
	}
	if req.AdvisorCacheProfile {
		messages := claudeSubMessages(req.Messages)
		s.sync.commitAdvisor(messages[:len(messages)-1])
	}
	if err := claudeSubConsume(ctx, s, emit); err != nil {
		// A consume error aborts the query's stream, so its reported usage no
		// longer applies. Pre-consume failures return above and keep it, because
		// the query is still running and the retry continues it.
		s.queryUsage = UsageStats{}
		return err
	}
	return nil
}

func claudeSubFinishPending(ctx context.Context, s *claudeSubSession, req ChatRequest) error {
	if len(s.pending) == 0 {
		return nil
	}
	provided := make(map[string]struct{})
	for _, msg := range req.Messages {
		if msg.Role == MessageRoleTool {
			provided[msg.ToolCallID] = struct{}{}
		}
	}
	var stale []claudeSubPendingCall
	for _, pending := range s.pending {
		if _, ok := provided[pending.ID]; !ok {
			stale = append(stale, pending)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if _, err := s.control.interrupt(ctx); err != nil {
		return err
	}
	s.sync.markInterrupted()
	s.queryUsage = UsageStats{}
	for _, pending := range stale {
		s.host.resolve(pending.Handle, claudeSubToolResult{Text: "tool call interrupted", IsError: true})
		claudeSubRemovePending(s, pending.ID)
	}
	return nil
}

func claudeSubPendingByID(s *claudeSubSession, id string) *claudeSubPendingCall {
	for i := range s.pending {
		if s.pending[i].ID == id {
			return &s.pending[i]
		}
	}
	return nil
}

func claudeSubRemovePending(s *claudeSubSession, id string) {
	for i := range s.pending {
		if s.pending[i].ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

func claudeSubToolResultFromMessage(msg Message) claudeSubToolResult {
	return claudeSubToolResult{Text: msg.Content, Images: msg.Images, IsError: false}
}

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
					s.sync.commitAssistant(*item.Message)
					claudeSubAddUsage(&s.queryUsage, item.Usage)
					if s.host != nil {
						s.host.settle()
					}
					if err := emit(ChatChunk{Delta: *item.Message, ContentSnapshot: true, Done: true, FinishReason: item.FinishReason, Usage: item.Usage}); err != nil {
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
				// The result's usage is cumulative over the whole CLI query. Tool-call
				// chunks already reported part of it, so the final chunk carries the rest.
				usage := claudeSubResidualUsage(item.Result.Usage, s.queryUsage)
				s.queryUsage = UsageStats{}
				if item.Result.Usage == nil && ordinary != nil {
					// The result carries no usage, so use this message's own usage.
					usage = ordinary.Usage
				}
				if ordinary != nil {
					s.sync.commitAssistant(*ordinary.Message)
					if err := emit(ChatChunk{Delta: *ordinary.Message, ContentSnapshot: true, Done: true, FinishReason: normalizeAnthropicFinishReason(item.Result.StopReason), Usage: usage}); err != nil {
						return err
					}
				} else if err := emit(ChatChunk{Done: true, FinishReason: normalizeAnthropicFinishReason(item.Result.StopReason), Usage: usage}); err != nil {
					return err
				}
				return nil
			}
		}
	}
}

// claudeSubAddUsage adds src to dst field by field. A nil src adds nothing.
func claudeSubAddUsage(dst *UsageStats, src *UsageStats) {
	if src == nil {
		return
	}
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
	dst.CacheCreationInputTokens += src.CacheCreationInputTokens
	dst.CacheReadInputTokens += src.CacheReadInputTokens
}

// claudeSubResidualUsage returns result minus reported, clamped at zero
// independently for each field. A nil result has no usage to report, so it
// yields nil. With nothing reported, the residual is the full result.
func claudeSubResidualUsage(result *UsageStats, reported UsageStats) *UsageStats {
	if result == nil {
		return nil
	}
	return &UsageStats{
		PromptTokens:             max(0, result.PromptTokens-reported.PromptTokens),
		CompletionTokens:         max(0, result.CompletionTokens-reported.CompletionTokens),
		TotalTokens:              max(0, result.TotalTokens-reported.TotalTokens),
		CacheCreationInputTokens: max(0, result.CacheCreationInputTokens-reported.CacheCreationInputTokens),
		CacheReadInputTokens:     max(0, result.CacheReadInputTokens-reported.CacheReadInputTokens),
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
	return fmt.Errorf("claude_subscription turn failed: %s: %s", r.Subtype, text)
}

var _ Provider = (*ClaudeSubscriptionProvider)(nil)
var _ StatefulTranscript = (*ClaudeSubscriptionProvider)(nil)
