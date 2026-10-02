package interactive

import (
	"encoding/json"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// delegateToolSet is the set of tool names that emit delegation lifecycle events.
// It is derived from the canonical source in the delegation package.
var delegateToolSet = buildDelegateToolSet()

func buildDelegateToolSet() map[string]bool {
	tools := make(map[string]bool)
	for _, name := range delegation.AllSpecializedDelegateTools() {
		tools[strings.ToLower(name)] = true
	}
	return tools
}

// isDelegateToolCall returns true if the tool name is a known delegate tool.
func isDelegateToolCall(name string) bool {
	return delegateToolSet[strings.ToLower(name)]
}

// isAdvisorToolCall returns true if the tool name is the advisor tool.
func isAdvisorToolCall(name string) bool {
	return strings.EqualFold(name, "advisor")
}

// taskFromArgs extracts the "task" string from a tool call arguments map.
func taskFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	if v, ok := args["task"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// advisorQuestionAndFilesFromArgs extracts the "question" string and "files"
// slice from an advisor tool call arguments map. Returns nil for files if absent
// or not a slice.
func advisorQuestionAndFilesFromArgs(args map[string]any) (question string, files []string) {
	if args == nil {
		return "", nil
	}
	if v, ok := args["question"]; ok {
		if s, ok := v.(string); ok {
			question = s
		}
	}
	if v, ok := args["files"]; ok {
		if arr, ok := v.([]any); ok {
			for _, elem := range arr {
				if s, ok := elem.(string); ok {
					files = append(files, s)
				}
			}
		}
	}
	return question, files
}

// toolResultError decodes a persisted tool result as a tool.JSONEnvelope and
// returns its error, if any. Returns nil for non-envelope or successful
// results (the common case).
func toolResultError(content string) error {
	var envelope tool.JSONEnvelope
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return nil
	}
	if envelope.OK || envelope.Error == nil {
		return nil
	}
	return envelope.Error
}

// convertImageBlocks converts agent.ImageBlock to output.ImageBlock.
func convertImageBlocks(blocks []agent.ImageBlock) []output.ImageBlock {
	if len(blocks) == 0 {
		return nil
	}
	converted := make([]output.ImageBlock, len(blocks))
	for i, b := range blocks {
		converted[i] = output.ImageBlock{
			ID:        b.ID,
			FilePath:  b.FilePath,
			MediaType: b.MediaType,
			Data:      b.Data,
			Width:     b.Width,
			Height:    b.Height,
			SizeBytes: b.SizeBytes,
		}
	}
	return converted
}

// replaySessionMessages replays conversation messages and emits display events
// so the TUI can reconstruct the session view on resume. Delegate tool calls
// emit delegation events; regular tool calls emit tool call events.
func (s *Session) replaySessionMessages(msgs []agent.Message) {
	s.replaySessionMessagesWithLedger(msgs, nil)
}

func (s *Session) replaySessionMessagesWithLedger(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry) {
	paired := pairedToolResultIDs(msgs)
	startedToolCalls := map[string]int{}
	pendingDelegates := map[string][]agent.ToolCall{}
	pendingAdvisors := map[string]agent.ToolCall{}
	acks := &replayAcks{}
	claimedLedger := make(map[int]bool)
	for _, msg := range msgs {
		if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" {
			continue
		}
		switch msg.Role {
		case agent.MessageRoleUser:
			s.replayUserMessage(msg, acks)
		case agent.MessageRoleAssistant:
			if msg.ReasoningContent != "" {
				s.events.Emit(output.NewThinkingChunkEventWithSource(0, msg.ReasoningContent, output.ChunkSourceAssistant))
			}
			s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
			s.replayAssistantToolCalls(msg.ToolCalls, pendingDelegates, pendingAdvisors, startedToolCalls, paired)
			for _, call := range msg.ToolCalls {
				if !isDelegateToolCall(call.Name) {
					continue
				}
				if _, ok := paired[call.ID]; ok {
					continue
				}
				if idx := matchingOutstandingLedger(ledger, claimedLedger, call.ID); idx >= 0 {
					s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
					s.emitAcceptedAdmission(call.ID, ledger[idx].AgentID, ledger[idx].BatchID, ledger[idx].Group)
					s.events.Emit(output.NewDelegationStartedEvent(ledger[idx].AgentID, taskFromArgs(call.Arguments)))
					startedToolCalls[call.ID]++
				}
			}
		case agent.MessageRoleSummary:
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:        "compaction",
				Severity:    "done",
				SummaryText: msg.Content,
			}))
		case agent.MessageRoleTool:
			if pending, ok := pendingDelegates[msg.ToolCallID]; ok && len(pending) > 0 && (msg.DelegationAdmission == nil || (msg.DelegationAdmission.Status != "accepted" && msg.DelegationAdmission.Status != "rejected")) {
				if idx := matchingOutstandingLedger(ledger, claimedLedger, msg.ToolCallID); idx >= 0 {
					s.emitAcceptedAdmission(msg.ToolCallID, ledger[idx].AgentID, ledger[idx].BatchID, ledger[idx].Group)
					s.events.Emit(output.NewDelegationStartedEvent(ledger[idx].AgentID, taskFromArgs(pending[0].Arguments)))
				}
			}
			s.replayToolResult(msg, pendingDelegates, pendingAdvisors, startedToolCalls, acks)
		}
	}
	s.replayUnresolvedAcks(acks)
}

func matchingOutstandingLedger(ledger []agent.SubAgentLedgerEntry, claimed map[int]bool, callID string) int {
	found := -1
	for i, entry := range ledger {
		if !claimed[i] && entry.ParentCallID == callID {
			if found >= 0 {
				return -1
			}
			found = i
		}
	}
	if found >= 0 {
		claimed[found] = true
	}
	return found
}

func (s *Session) emitAcceptedAdmission(callID, agentID, batchID, group string) {
	s.events.Emit(output.NewDelegationAcceptedEvent(callID, agentID, batchID, group))
}

// replayUserMessage emits skill state, sub-agent delivery and user input
// events for one replayed user message.
func (s *Session) replayUserMessage(msg agent.Message, acks *replayAcks) {
	images := convertImageBlocks(msg.Images)
	parts := prompt.SplitMessageBlocks(msg.Content)
	for _, block := range parts.SkillBlocks {
		state := output.SkillStateDisabled
		if block.State == prompt.SkillBlockActive {
			state = output.SkillStateEnabled
		}
		s.events.Emit(output.NewSkillStateEvent(block.Name, state))
	}
	var delivered []output.DeliveredSubAgent
	for _, envelope := range parts.ResultEnvelopes {
		parsed, ok := agent.ParseSubAgentResultEnvelope(envelope)
		if !ok {
			continue
		}
		s.replaySubAgentResult(parsed, acks)
		_, usage := splitResultEnvelopeInner(parsed.Inner)
		delivered = append(delivered, output.DeliveredSubAgent{
			AgentID:      parsed.AgentID,
			AgentType:    parsed.AgentType,
			Status:       parsed.Status,
			ParentCallID: parsed.CallID,
			DurationMs:   usage.duration.Milliseconds(),
		})
	}
	if len(delivered) > 0 {
		s.events.Emit(output.NewSubAgentsDeliveredEvent(delivered))
	}
	// Rest already excludes the mode notice prefix, every block, the
	// pending line and every result envelope, so it replaces
	// StripModeNotice here. Emit the input event only when user text or
	// images remain.
	if strings.TrimSpace(parts.Rest) != "" || len(images) > 0 {
		s.events.Emit(output.NewUserInputEvent(parts.Rest, "resume", images))
	}
}

// replayAssistantToolCalls emits events for each tool call in an assistant message.
// Only tool calls with a paired tool result are emitted; orphaned calls (e.g. an
// accepted workflow_handoff that stops the run without appending a result) are
// skipped so the TUI does not show them as still-running.
func (s *Session) replayAssistantToolCalls(calls []agent.ToolCall, pendingDelegates map[string][]agent.ToolCall, pendingAdvisors map[string]agent.ToolCall, startedToolCalls map[string]int, paired map[string]struct{}) {
	for _, call := range calls {
		if isAdvisorToolCall(call.Name) {
			if _, ok := paired[call.ID]; !ok {
				continue
			}
			pendingAdvisors[call.ID] = call
		} else if isDelegateToolCall(call.Name) {
			if _, ok := paired[call.ID]; !ok {
				continue
			}
			pendingDelegates[call.ID] = append(pendingDelegates[call.ID], call)
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			startedToolCalls[call.ID]++
		} else if _, ok := paired[call.ID]; ok {
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			startedToolCalls[call.ID]++
		}
	}
}

// pairedToolResultIDs returns the set of tool call IDs that have a matching
// tool result message in msgs. Tool calls absent from this set stopped the run
// without producing a result (e.g. an accepted workflow_handoff).
func pairedToolResultIDs(msgs []agent.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, msg := range msgs {
		if msg.Role == agent.MessageRoleTool && msg.ToolCallID != "" {
			ids[msg.ToolCallID] = struct{}{}
		}
	}
	return ids
}

// replayToolResult emits the completion event for a tool result message.
func (s *Session) replayToolResult(msg agent.Message, pendingDelegates map[string][]agent.ToolCall, pendingAdvisors map[string]agent.ToolCall, startedToolCalls map[string]int, acks *replayAcks) {
	if pending, ok := pendingAdvisors[msg.ToolCallID]; ok {
		question, files := advisorQuestionAndFilesFromArgs(pending.Arguments)
		s.events.Emit(output.NewAdvisorStartedEvent("", 0, 0, question, files))
		s.events.Emit(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Note: msg.Content}))
		delete(pendingAdvisors, msg.ToolCallID)
	} else if pending, ok := pendingDelegates[msg.ToolCallID]; ok && len(pending) > 0 {
		call := pending[0]
		pendingDelegates[msg.ToolCallID] = pending[1:]
		if len(pendingDelegates[msg.ToolCallID]) == 0 {
			delete(pendingDelegates, msg.ToolCallID)
		}
		task := taskFromArgs(call.Arguments)
		admission := msg.DelegationAdmission
		if admission != nil && admission.Status == "rejected" {
		} else {
			state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
			if admission != nil && admission.Status == "accepted" {
				s.emitAcceptedAdmission(msg.ToolCallID, admission.AgentID, admission.BatchID, admission.Group)
			}
			if state.status == "queued" {
				s.events.Emit(output.NewDelegationQueuedEvent(state.agentID, msg.ToolCallID, "", task))
			} else {
				s.events.Emit(output.NewDelegationStartedEvent(state.agentID, task))
			}
			switch {
			case isAckStatus(state.status):
				acks.add(msg.ToolCallID, state.agentID, task)
			case state.status == "failed":
				s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{
					AgentID:     state.agentID,
					TaskPreview: task,
					Error:       state.error,
				}))
			default:
				s.events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
					AgentID:           state.agentID,
					Status:            state.status,
					TurnCount:         state.turnCount,
					TokenCount:        state.tokenCount,
					ToolCallCount:     state.toolCallCount,
					Output:            state.output,
					InputTokens:       state.inputTokens,
					CacheReadTokens:   state.cacheReadTokens,
					CacheCreateTokens: state.cacheCreateTokens,
				}))
			}
		}
	}

	// Emit DisplayFileEvent if this is a display_file call with valid payload.
	if msg.Name == "display_file" {
		var result builtin.DisplayFileResult
		if err := json.Unmarshal([]byte(msg.Content), &result); err == nil && result.Path != "" {
			placeholder := strings.TrimSpace(result.Message)
			if placeholder == "" {
				placeholder = "(file content not available after resume)"
			}
			s.events.Emit(output.NewDisplayFileEvent(output.DisplayFilePayload{
				Path:    result.Path,
				Preview: output.FormatFilePreview(result.Path, placeholder),
			}))
		}
	}

	// Always emit ToolCallFinishedEvent for any tool call that was started.
	if startedToolCalls[msg.ToolCallID] > 0 && (!isDelegateToolCall(msg.Name) || (msg.DelegationAdmission != nil && (msg.DelegationAdmission.Status == "accepted" || msg.DelegationAdmission.Status == "rejected"))) {
		if msg.DelegationAdmission != nil && (msg.DelegationAdmission.Status == "accepted" || msg.DelegationAdmission.Status == "rejected") {
			s.events.Emit(output.NewToolCallFinishedEventWithAdmission(0, msg.Name, msg.ToolCallID, msg.Content, toolResultError(msg.Content), output.ToolPreview{}, &output.DelegationAdmission{
				Status: msg.DelegationAdmission.Status, BatchID: msg.DelegationAdmission.BatchID, Group: msg.DelegationAdmission.Group,
				AgentID: msg.DelegationAdmission.AgentID, PolicyNotice: msg.DelegationAdmission.PolicyNotice,
			}))
		} else {
			s.events.Emit(output.NewToolCallFinishedEvent(0, msg.Name, msg.ToolCallID, msg.Content, toolResultError(msg.Content)))
		}
		startedToolCalls[msg.ToolCallID]--
		if startedToolCalls[msg.ToolCallID] == 0 {
			delete(startedToolCalls, msg.ToolCallID)
		}
	}
}
