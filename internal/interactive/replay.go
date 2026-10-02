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
	plan := buildReplayOccurrencePlan(msgs, ledger)
	startedToolCalls := make(map[replayOccurrenceKey]bool)
	acks := &replayAcks{}
	for messageIndex, msg := range msgs {
		if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" {
			continue
		}
		s.replayMessage(msg, replayState{startedToolCalls: startedToolCalls, acks: acks}, replayLedger{entries: ledger, occurrences: plan.occurrences, owners: plan.resultOwners, messageIndex: messageIndex})
	}
	s.replayUnresolvedAcks(acks)
}

type replayOccurrenceKey struct {
	messageIndex int
	callIndex    int
}

type replayOccurrence struct {
	call               agent.ToolCall
	resultMessageIndex int
	ledgerIndex        int
}

type replayOccurrencePlan struct {
	occurrences    map[replayOccurrenceKey]*replayOccurrence
	resultOwners   map[int]replayOccurrenceKey
	reservedLedger map[int]bool
}

func buildReplayOccurrencePlan(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry) replayOccurrencePlan {
	plan := replayOccurrencePlan{occurrences: make(map[replayOccurrenceKey]*replayOccurrence), resultOwners: make(map[int]replayOccurrenceKey)}
	queues := make(map[string][]replayOccurrenceKey)
	for messageIndex, msg := range msgs {
		if msg.Role == agent.MessageRoleAssistant {
			for callIndex, call := range msg.ToolCalls {
				key := replayOccurrenceKey{messageIndex: messageIndex, callIndex: callIndex}
				plan.occurrences[key] = &replayOccurrence{call: call, resultMessageIndex: -1, ledgerIndex: -1}
				if call.ID != "" {
					queues[call.ID] = append(queues[call.ID], key)
				}
			}
		}
		if msg.Role != agent.MessageRoleTool || msg.ToolCallID == "" {
			continue
		}
		queue := queues[msg.ToolCallID]
		for len(queue) > 0 && plan.occurrences[queue[0]].resultMessageIndex >= 0 {
			queue = queue[1:]
		}
		if len(queue) == 0 {
			continue
		}
		key := queue[0]
		queue = queue[1:]
		queues[msg.ToolCallID] = queue
		plan.occurrences[key].resultMessageIndex = messageIndex
		plan.resultOwners[messageIndex] = key
	}
	assignExplicitReplayLedgerOwnership(msgs, ledger, &plan)
	assignFallbackReplayLedgerOwnership(msgs, ledger, &plan)
	return plan
}

type replayState struct {
	startedToolCalls map[replayOccurrenceKey]bool
	acks             *replayAcks
}

type replayLedger struct {
	entries      []agent.SubAgentLedgerEntry
	occurrences  map[replayOccurrenceKey]*replayOccurrence
	owners       map[int]replayOccurrenceKey
	messageIndex int
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
		owner, hasOwner := ledger.owners[ledger.messageIndex]
		var occurrence *replayOccurrence
		if hasOwner {
			occurrence = ledger.occurrences[owner]
		}
		if hasOwner && occurrence != nil {
			s.replayLedgerAdmission(msg, occurrence, ledger)
			s.replayOwnedToolResult(msg, owner, occurrence.call, state, ledgerEntryForOccurrence(ledger, occurrence))
		} else {
			s.replayDisplayFile(msg)
		}
	}
}

func (s *Session) replayAssistantMessage(msg agent.Message, state replayState, ledger replayLedger) {
	if msg.ReasoningContent != "" {
		s.events.Emit(output.NewThinkingChunkEventWithSource(0, msg.ReasoningContent, output.ChunkSourceAssistant))
	}
	s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
	for callIndex, call := range msg.ToolCalls {
		key := replayOccurrenceKey{messageIndex: ledger.messageIndex, callIndex: callIndex}
		occurrence := ledger.occurrences[key]
		if occurrence == nil {
			continue
		}
		if occurrence.resultMessageIndex >= 0 {
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			state.startedToolCalls[key] = true
			continue
		}
		if occurrence.ledgerIndex >= 0 && isDelegateToolCall(call.Name) {
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			entry := ledger.entries[occurrence.ledgerIndex]
			s.emitAcceptedAdmission(call.ID, entry.AgentID, entry.BatchID, entry.Group)
			s.events.Emit(output.NewDelegationStartedEvent(entry.AgentID, taskFromArgs(call.Arguments), call.ID))
			state.startedToolCalls[key] = true
		}
	}
}

func (s *Session) replayLedgerAdmission(msg agent.Message, occurrence *replayOccurrence, ledger replayLedger) {
	if occurrence == nil || occurrence.ledgerIndex < 0 {
		return
	}
	admission := msg.DelegationAdmission
	if hasKnownAdmission(admission) {
		return
	}
	entry := ledger.entries[occurrence.ledgerIndex]
	status := replayStatus(msg)
	if status != "running" && status != "queued" {
		return
	}
	s.emitAcceptedAdmission(msg.ToolCallID, entry.AgentID, entry.BatchID, entry.Group)
}

func ledgerEntryForOccurrence(ledger replayLedger, occurrence *replayOccurrence) agent.SubAgentLedgerEntry {
	if occurrence == nil || occurrence.ledgerIndex < 0 {
		return agent.SubAgentLedgerEntry{}
	}
	return ledger.entries[occurrence.ledgerIndex]
}

func assignExplicitReplayLedgerOwnership(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry, plan *replayOccurrencePlan) {
	reserved := make(map[int]bool)
	ledgerCandidates := make(map[replayOccurrenceKey][]int)
	entryDegrees := make(map[int]int)
	for key, occurrence := range plan.occurrences {
		if occurrence.resultMessageIndex < 0 {
			continue
		}
		admission := msgs[occurrence.resultMessageIndex].DelegationAdmission
		if admission == nil || admission.Status != "accepted" || admission.AgentID == "" {
			continue
		}
		candidates := explicitLedgerCandidates(occurrence.call.ID, admission, ledger)
		if len(candidates) == 0 {
			continue
		}
		ledgerCandidates[key] = candidates
		for _, index := range candidates {
			reserved[index] = true
			entryDegrees[index]++
		}
	}
	for key, candidates := range ledgerCandidates {
		if len(candidates) == 1 && entryDegrees[candidates[0]] == 1 {
			plan.occurrences[key].ledgerIndex = candidates[0]
		}
	}
	plan.reservedLedger = reserved
}

func explicitLedgerCandidates(callID string, admission *tool.DelegationAdmission, ledger []agent.SubAgentLedgerEntry) []int {
	var candidates []int
	for i, entry := range ledger {
		if entry.ParentCallID == callID && entry.AgentID == admission.AgentID && optionalMatches(admission.BatchID, entry.BatchID) && optionalMatches(admission.Group, entry.Group) {
			candidates = append(candidates, i)
		}
	}
	return candidates
}

func assignFallbackReplayLedgerOwnership(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry, plan *replayOccurrencePlan) {
	buckets := make(map[string][]*replayOccurrence)
	for _, occurrence := range plan.occurrences {
		if occurrence.ledgerIndex >= 0 || !isDelegateToolCall(occurrence.call.Name) {
			continue
		}
		eligible := occurrence.resultMessageIndex < 0
		if occurrence.resultMessageIndex >= 0 {
			msg := msgs[occurrence.resultMessageIndex]
			status := replayStatus(msg)
			eligible = !hasKnownAdmission(msg.DelegationAdmission) && (status == "running" || status == "queued")
		}
		if eligible {
			buckets[occurrence.call.ID] = append(buckets[occurrence.call.ID], occurrence)
		}
	}
	for parentCallID, occurrences := range buckets {
		var candidates []int
		for i, entry := range ledger {
			if !plan.reservedLedger[i] && entry.ParentCallID == parentCallID {
				candidates = append(candidates, i)
			}
		}
		if len(occurrences) == 1 && len(candidates) == 1 {
			occurrences[0].ledgerIndex = candidates[0]
			plan.reservedLedger[candidates[0]] = true
		}
	}
}

func replayStatus(msg agent.Message) string {
	if msg.Retention != nil && msg.Retention.Status != "" {
		return msg.Retention.Status
	}
	state := buildReplayedDelegationState(msg.ToolCallID, nil, msg.Content)
	return state.status
}

func optionalMatches(authoritative, evidence string) bool {
	return authoritative == "" || evidence == "" || authoritative == evidence
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

func (s *Session) replayOwnedToolResult(msg agent.Message, owner replayOccurrenceKey, call agent.ToolCall, state replayState, inferred agent.SubAgentLedgerEntry) {
	if isAdvisorToolCall(call.Name) {
		question, files := advisorQuestionAndFilesFromArgs(call.Arguments)
		s.events.Emit(output.NewAdvisorStartedEvent("", 0, 0, question, files))
		s.events.Emit(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Note: msg.Content}))
	} else if isDelegateToolCall(call.Name) {
		s.replayDelegateResult(msg, call, state.acks, inferred)
	}
	s.replayDisplayFile(msg)
	if state.startedToolCalls[owner] {
		s.replayToolFinished(msg)
		delete(state.startedToolCalls, owner)
	}
}

func (s *Session) replayDelegateResult(msg agent.Message, call agent.ToolCall, acks *replayAcks, inferred agent.SubAgentLedgerEntry) {
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
	if replayLedgerBackedAck(inferred, state.status) {
		s.emitLedgerProgress(msg, inferred, task, state.status)
		return
	}
	if s.replayEmptyAcceptedFailure(msg, state, task) {
		return
	}
	s.emitReplayProgress(msg, state, task)
	s.replayDelegationTerminal(msg, state, task, acks, inferred)
}

func replayLedgerBackedAck(inferred agent.SubAgentLedgerEntry, status string) bool {
	return inferred.AgentID != "" && isAckStatus(status)
}

func (s *Session) emitLedgerProgress(msg agent.Message, inferred agent.SubAgentLedgerEntry, task, status string) {
	agentID := inferred.AgentID
	if hasKnownAdmission(msg.DelegationAdmission) && msg.DelegationAdmission.Status == "accepted" && msg.DelegationAdmission.AgentID != "" {
		agentID = msg.DelegationAdmission.AgentID
	}
	s.emitDelegationProgress(agentID, msg.ToolCallID, task, status)
}

func (s *Session) replayEmptyAcceptedFailure(msg agent.Message, state replayedDelegationState, task string) bool {
	if state.status != "failed" || state.output != "" || !hasKnownAdmission(msg.DelegationAdmission) || msg.DelegationAdmission.Status != "accepted" {
		return false
	}
	s.emitDelegationFailure(state.agentID, msg, task, state.error)
	return true
}

func (s *Session) emitReplayProgress(msg agent.Message, state replayedDelegationState, task string) {
	if hasKnownAdmission(msg.DelegationAdmission) && msg.DelegationAdmission.Status == "accepted" {
		s.emitDelegationProgress(state.agentID, msg.ToolCallID, task, state.status)
	} else {
		s.emitLegacyDelegationProgress(state.agentID, task, state.status, msg.ToolCallID)
	}
}

func (s *Session) replayDelegationTerminal(msg agent.Message, state replayedDelegationState, task string, acks *replayAcks, inferred agent.SubAgentLedgerEntry) {
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
		s.events.Emit(output.NewDelegationStartedEvent(agentID, task, callID))
	}
}

func (s *Session) emitLegacyDelegationProgress(agentID, task, status, callID string) {
	if status == "queued" {
		s.events.Emit(output.NewDelegationQueuedEvent(agentID, callID, "", task))
	} else {
		s.events.Emit(output.NewDelegationStartedEvent(agentID, task))
	}
}

func (s *Session) emitDelegationFailure(agentID string, msg agent.Message, task, err string) {
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == "accepted" && msg.DelegationAdmission.AgentID != "" {
		agentID = msg.DelegationAdmission.AgentID
	}
	callID := ""
	if msg.DelegationAdmission != nil && msg.DelegationAdmission.Status == "accepted" {
		callID = msg.ToolCallID
	}
	s.events.Emit(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: agentID, CallID: callID, TaskPreview: task, Error: err}))
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

func (s *Session) replayToolFinished(msg agent.Message) {
	if !isDelegateToolCall(msg.Name) || hasKnownAdmission(msg.DelegationAdmission) {
		if hasKnownAdmission(msg.DelegationAdmission) {
			s.events.Emit(output.NewToolCallFinishedEventWithAdmission(0, msg.Name, msg.ToolCallID, msg.Content, toolResultError(msg.Content), output.ToolPreview{}, &output.DelegationAdmission{
				Status: msg.DelegationAdmission.Status, BatchID: msg.DelegationAdmission.BatchID, Group: msg.DelegationAdmission.Group,
				AgentID: msg.DelegationAdmission.AgentID, PolicyNotice: msg.DelegationAdmission.PolicyNotice,
			}))
		} else {
			s.events.Emit(output.NewToolCallFinishedEvent(0, msg.Name, msg.ToolCallID, msg.Content, toolResultError(msg.Content)))
		}
	}
}
