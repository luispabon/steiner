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
	paired := pairedToolResultIDs(msgs)
	startedToolCalls := map[string]struct{}{}
	pendingDelegates := map[string]agent.ToolCall{}
	pendingAdvisors := map[string]agent.ToolCall{}
	for _, msg := range msgs {
		if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" {
			continue
		}
		switch msg.Role {
		case agent.MessageRoleUser:
			images := convertImageBlocks(msg.Images)
			s.events.Emit(output.NewUserInputEvent(prompt.StripModeNotice(msg.Content), "resume", images))
		case agent.MessageRoleAssistant:
			if msg.ReasoningContent != "" {
				s.events.Emit(output.NewThinkingChunkEventWithSource(0, msg.ReasoningContent, output.ChunkSourceAssistant))
			}
			s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
			s.replayAssistantToolCalls(msg.ToolCalls, pendingDelegates, pendingAdvisors, startedToolCalls, paired)
		case agent.MessageRoleSummary:
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:        "compaction",
				Severity:    "done",
				SummaryText: msg.Content,
			}))
		case agent.MessageRoleTool:
			s.replayToolResult(msg, pendingDelegates, pendingAdvisors, startedToolCalls)
		}
	}
}

// replayAssistantToolCalls emits events for each tool call in an assistant message.
// Only tool calls with a paired tool result are emitted; orphaned calls (e.g. an
// accepted workflow_handoff that stops the run without appending a result) are
// skipped so the TUI does not show them as still-running.
func (s *Session) replayAssistantToolCalls(calls []agent.ToolCall, pendingDelegates map[string]agent.ToolCall, pendingAdvisors map[string]agent.ToolCall, startedToolCalls map[string]struct{}, paired map[string]struct{}) {
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
			pendingDelegates[call.ID] = call
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			startedToolCalls[call.ID] = struct{}{}
		} else if _, ok := paired[call.ID]; ok {
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			startedToolCalls[call.ID] = struct{}{}
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
func (s *Session) replayToolResult(msg agent.Message, pendingDelegates map[string]agent.ToolCall, pendingAdvisors map[string]agent.ToolCall, startedToolCalls map[string]struct{}) {
	if pending, ok := pendingAdvisors[msg.ToolCallID]; ok {
		question, files := advisorQuestionAndFilesFromArgs(pending.Arguments)
		s.events.Emit(output.NewAdvisorStartedEvent("", 0, 0, question, files))
		s.events.Emit(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Note: msg.Content}))
		delete(pendingAdvisors, msg.ToolCallID)
	} else if pending, ok := pendingDelegates[msg.ToolCallID]; ok {
		state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
		task := taskFromArgs(pending.Arguments)
		s.events.Emit(output.NewDelegationStartedEvent(state.agentID, task))
		if state.status == "failed" {
			s.events.Emit(output.NewDelegationFailedEvent(state.agentID, task, state.error))
		} else {
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
		delete(pendingDelegates, msg.ToolCallID)
	} else if msg.Name == "display_file" {
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
	} else if _, ok := startedToolCalls[msg.ToolCallID]; ok {
		s.events.Emit(output.NewToolCallFinishedEvent(0, msg.Name, msg.ToolCallID, msg.Content, toolResultError(msg.Content)))
	}
}
