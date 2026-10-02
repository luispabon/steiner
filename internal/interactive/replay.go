package interactive

import (
	"encoding/json"
	"errors"
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
		var projected struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(content), &projected); err == nil && (projected.Status == "failed" || projected.Status == "cancelled" || projected.Status == "lost") && projected.Reason != "" {
			return errors.New(projected.Reason)
		}
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
		s.replayMessage(msg, replayState{pendingDelegates, pendingAdvisors, startedToolCalls, acks}, replayLedger{entries: ledger, claimed: claimedLedger, paired: paired, inferred: make(map[string]agent.SubAgentLedgerEntry)})
	}
	s.replayUnresolvedAcks(acks)
}

type replayState struct {
	pendingDelegates map[string][]agent.ToolCall
	pendingAdvisors  map[string]agent.ToolCall
	startedToolCalls map[string]int
	acks             *replayAcks
}

type replayLedger struct {
	entries  []agent.SubAgentLedgerEntry
	claimed  map[int]bool
	paired   map[string]int
	inferred map[string]agent.SubAgentLedgerEntry
}

func (s *Session) replayMessage(msg agent.Message, state replayState, ledger replayLedger) {
	switch msg.Role {
	case agent.MessageRoleUser:
		s.replayUserMessage(msg, state.acks)
	case agent.MessageRoleAssistant:
		s.replayAssistantMessage(msg, state, ledger)
	case agent.MessageRoleSummary:
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{Kind: "compaction", Severity: "done", SummaryText: msg.Content}))
	case agent.MessageRoleTool:
		s.replayLedgerAdmission(msg, state.pendingDelegates, ledger)
		s.replayToolResult(msg, state.pendingDelegates, state.pendingAdvisors, state.startedToolCalls, state.acks, ledger.inferred[msg.ToolCallID])
	}
}

func (s *Session) replayAssistantMessage(msg agent.Message, state replayState, ledger replayLedger) {
	if msg.ReasoningContent != "" {
		s.events.Emit(output.NewThinkingChunkEventWithSource(0, msg.ReasoningContent, output.ChunkSourceAssistant))
	}
	s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
	orphans := s.replayAssistantToolCalls(msg.ToolCalls, state.pendingDelegates, state.pendingAdvisors, state.startedToolCalls, ledger.paired)
	for _, call := range orphans {
		idx := matchingOutstandingLedger(ledger.entries, ledger.claimed, call.ID)
		if idx < 0 {
			continue
		}
		s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
		s.emitAcceptedAdmission(call.ID, ledger.entries[idx].AgentID, ledger.entries[idx].BatchID, ledger.entries[idx].Group)
		s.events.Emit(output.NewDelegationStartedEvent(ledger.entries[idx].AgentID, taskFromArgs(call.Arguments)))
		state.startedToolCalls[call.ID]++
	}
}

func (s *Session) replayLedgerAdmission(msg agent.Message, pending map[string][]agent.ToolCall, ledger replayLedger) {
	if hasKnownAdmission(msg.DelegationAdmission) {
		if msg.DelegationAdmission.Status == "accepted" {
			if idx := claimLedgerOccurrence(ledger.entries, ledger.claimed, msg.ToolCallID); idx >= 0 {
				ledger.inferred[msg.ToolCallID] = ledger.entries[idx]
			}
		}
		return
	}
	calls := pending[msg.ToolCallID]
	if len(calls) == 0 {
		return
	}
	idx := matchingOutstandingLedger(ledger.entries, ledger.claimed, msg.ToolCallID)
	if idx < 0 {
		return
	}
	ledger.inferred[msg.ToolCallID] = ledger.entries[idx]
	s.emitAcceptedAdmission(msg.ToolCallID, ledger.entries[idx].AgentID, ledger.entries[idx].BatchID, ledger.entries[idx].Group)
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
func (s *Session) replayAssistantToolCalls(calls []agent.ToolCall, pendingDelegates map[string][]agent.ToolCall, pendingAdvisors map[string]agent.ToolCall, startedToolCalls map[string]int, paired map[string]int) []agent.ToolCall {
	var orphans []agent.ToolCall
	for _, call := range calls {
		switch {
		case isAdvisorToolCall(call.Name):
			if paired[call.ID] > 0 {
				paired[call.ID]--
				pendingAdvisors[call.ID] = call
			}
		case isDelegateToolCall(call.Name):
			if paired[call.ID] <= 0 {
				orphans = append(orphans, call)
				continue
			}
			paired[call.ID]--
			pendingDelegates[call.ID] = append(pendingDelegates[call.ID], call)
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			startedToolCalls[call.ID]++
		default:
			if paired[call.ID] > 0 {
				paired[call.ID]--
				s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
				startedToolCalls[call.ID]++
			}
		}
	}
	return orphans
}

// pairedToolResultIDs returns the set of tool call IDs that have a matching
// tool result message in msgs. Tool calls absent from this set stopped the run
// without producing a result (e.g. an accepted workflow_handoff).
func pairedToolResultIDs(msgs []agent.Message) map[string]int {
	ids := make(map[string]int)
	for _, msg := range msgs {
		if msg.Role == agent.MessageRoleTool && msg.ToolCallID != "" {
			ids[msg.ToolCallID]++
		}
	}
	return ids
}

// replayToolResult emits the completion event for a tool result message.
func (s *Session) replayToolResult(msg agent.Message, pendingDelegates map[string][]agent.ToolCall, pendingAdvisors map[string]agent.ToolCall, startedToolCalls map[string]int, acks *replayAcks, inferred agent.SubAgentLedgerEntry) {
	if pending, ok := pendingAdvisors[msg.ToolCallID]; ok {
		s.replayAdvisorResult(msg, pending, pendingAdvisors)
	} else if pending, ok := pendingDelegates[msg.ToolCallID]; ok && len(pending) > 0 {
		s.replayDelegateResult(msg, pending[0], pendingDelegates, acks, inferred)
	}

	s.replayDisplayFile(msg)
	s.replayToolFinished(msg, startedToolCalls)
}

func (s *Session) replayAdvisorResult(msg agent.Message, pending agent.ToolCall, advisors map[string]agent.ToolCall) {
	question, files := advisorQuestionAndFilesFromArgs(pending.Arguments)
	s.events.Emit(output.NewAdvisorStartedEvent("", 0, 0, question, files))
	s.events.Emit(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Note: msg.Content}))
	delete(advisors, msg.ToolCallID)
}

func (s *Session) replayDelegateResult(msg agent.Message, call agent.ToolCall, pending map[string][]agent.ToolCall, acks *replayAcks, inferred agent.SubAgentLedgerEntry) {
	calls := pending[msg.ToolCallID][1:]
	if len(calls) == 0 {
		delete(pending, msg.ToolCallID)
	} else {
		pending[msg.ToolCallID] = calls
	}
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == "rejected" {
		return
	}
	task := taskFromArgs(call.Arguments)
	state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
	if hasKnownAdmission(msg.DelegationAdmission) && msg.DelegationAdmission.Status == "accepted" {
		s.emitAcceptedAdmission(msg.ToolCallID, msg.DelegationAdmission.AgentID, msg.DelegationAdmission.BatchID, msg.DelegationAdmission.Group)
		if msg.DelegationAdmission.AgentID != "" {
			state.agentID = msg.DelegationAdmission.AgentID
		}
	}
	if inferred.AgentID != "" && isAckStatus(state.status) {
		s.emitDelegationProgress(inferred.AgentID, msg.ToolCallID, task, state.status)
		return
	}
	if state.status == "failed" && state.output == "" {
		s.emitDelegationFailure(state.agentID, msg, task, state.error)
		return
	}
	s.emitDelegationProgress(state.agentID, msg.ToolCallID, task, state.status)
	switch {
	case isAckStatus(state.status):
		if inferred.AgentID == "" {
			acks.add(msg.ToolCallID, state.agentID, task)
		}
	case state.status == "failed":
		s.emitDelegationFailure(state.agentID, msg, task, state.error)
	default:
		s.emitDelegationComplete(state)
	}
}

func (s *Session) emitDelegationProgress(agentID, callID, task, status string) {
	if status == "queued" {
		s.events.Emit(output.NewDelegationQueuedEvent(agentID, callID, "", task))
	} else {
		s.events.Emit(output.NewDelegationStartedEvent(agentID, task))
	}
}

func (s *Session) emitDelegationFailure(agentID string, msg agent.Message, task, err string) {
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.AgentID != "" {
		agentID = msg.DelegationAdmission.AgentID
	}
	s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: agentID, TaskPreview: task, Error: err}))
}

func claimLedgerOccurrence(ledger []agent.SubAgentLedgerEntry, claimed map[int]bool, callID string) int {
	for i, entry := range ledger {
		if !claimed[i] && entry.ParentCallID == callID {
			claimed[i] = true
			return i
		}
	}
	return -1
}

func (s *Session) emitDelegationComplete(state replayedDelegationState) {
	s.events.Emit(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: state.agentID, Status: state.status, TurnCount: state.turnCount, TokenCount: state.tokenCount, ToolCallCount: state.toolCallCount, Output: state.output, InputTokens: state.inputTokens, CacheReadTokens: state.cacheReadTokens, CacheCreateTokens: state.cacheCreateTokens}))
}

func (s *Session) replayDisplayFile(msg agent.Message) {
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

}

func hasKnownAdmission(admission *tool.DelegationAdmission) bool {
	return admission != nil && (admission.Status == "accepted" || admission.Status == "rejected")
}

func (s *Session) replayToolFinished(msg agent.Message, startedToolCalls map[string]int) {
	if startedToolCalls[msg.ToolCallID] > 0 && (!isDelegateToolCall(msg.Name) || hasKnownAdmission(msg.DelegationAdmission)) {
		if hasKnownAdmission(msg.DelegationAdmission) {
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
