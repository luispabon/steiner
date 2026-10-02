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
// emit delegation events; regular tool calls emit tool call events. The ledger
// ties outstanding sub-agents to their originating call occurrences.
func (s *Session) replaySessionMessages(msgs []agent.Message, ledger []agent.SubAgentLedgerEntry) {
	plan := buildReplayOccurrencePlan(msgs, ledger)
	state := replayState{
		startedToolCalls: make(map[replayOccurrenceKey]bool),
		acks:             &replayAcks{},
		consumedResults:  make(map[int]bool),
		msgs:             msgs,
	}
	for messageIndex, msg := range msgs {
		if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" {
			continue
		}
		s.replayMessage(msg, state, replayLedger{entries: ledger, occurrences: plan.occurrences, owners: plan.resultOwners, messageIndex: messageIndex})
	}
	s.replayUnresolvedAcks(state.acks)
}

type replayState struct {
	startedToolCalls map[replayOccurrenceKey]bool
	acks             *replayAcks
	consumedResults  map[int]bool
	msgs             []agent.Message
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
		if state.consumedResults[ledger.messageIndex] {
			return
		}
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
			if isDelegateToolCall(call.Name) && replayBundlesOccurrence(state, occurrence) {
				s.replayDelegationBundle(call, key, occurrence, state, ledger)
				continue
			}
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			state.startedToolCalls[key] = true
			continue
		}
		if occurrence.ledgerIndex >= 0 && isDelegateToolCall(call.Name) {
			s.replayLedgerOrphanBundle(call, key, occurrence, state, ledger)
		}
	}
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

func (s *Session) replayToolFinished(msg agent.Message) {
	known := hasKnownAdmission(msg.DelegationAdmission)
	if isDelegateToolCall(msg.Name) && !known {
		return
	}
	var admission *output.DelegationAdmission
	if known {
		admission = (*output.DelegationAdmission)(msg.DelegationAdmission)
	}
	s.events.Emit(output.NewToolCallFinishedEventWithAdmission(0, msg.Name, msg.ToolCallID, msg.Content, toolResultError(msg.Content), output.ToolPreview{}, admission))
}
