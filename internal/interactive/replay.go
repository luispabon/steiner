package interactive

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// isDelegateToolCall returns true if the tool name is a known delegate tool.
func isDelegateToolCall(name string) bool {
	switch name {
	case "delegate", "explore", "research", "code", "plan", "verify":
		return true
	}
	return false
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

// replaySessionMessages replays conversation messages and emits display events
// so the TUI can reconstruct the session view on resume. Delegate tool calls
// emit delegation events; regular tool calls emit tool call events.
func (s *Session) replaySessionMessages(msgs []agent.Message) {
	paired := pairedToolResultIDs(msgs)
	startedToolCalls := map[string]struct{}{}
	pendingDelegates := map[string]agent.ToolCall{}
	for _, msg := range msgs {
		if msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" {
			continue
		}
		switch msg.Role {
		case agent.MessageRoleUser:
			s.events.Emit(output.NewUserInputEvent(msg.Content, "resume"))
		case agent.MessageRoleAssistant:
			s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
			s.replayAssistantToolCalls(msg.ToolCalls, pendingDelegates, startedToolCalls, paired)
		case agent.MessageRoleTool:
			s.replayToolResult(msg, pendingDelegates, startedToolCalls)
		}
	}
}

// replayAssistantToolCalls emits events for each tool call in an assistant message.
// Only tool calls with a paired tool result are emitted; orphaned calls (e.g. an
// accepted workflow_handoff that stops the run without appending a result) are
// skipped so the TUI does not show them as still-running.
func (s *Session) replayAssistantToolCalls(calls []agent.ToolCall, pendingDelegates map[string]agent.ToolCall, startedToolCalls map[string]struct{}, paired map[string]struct{}) {
	for _, call := range calls {
		if isDelegateToolCall(call.Name) {
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
func (s *Session) replayToolResult(msg agent.Message, pendingDelegates map[string]agent.ToolCall, startedToolCalls map[string]struct{}) {
	if pending, ok := pendingDelegates[msg.ToolCallID]; ok {
		state := buildReplayedDelegationState(msg.ToolCallID, msg.Retention, msg.Content)
		task := taskFromArgs(pending.Arguments)
		s.events.Emit(output.NewDelegationStartedEvent(state.agentID, task))
		if state.status == "failed" {
			s.events.Emit(output.NewDelegationFailedEvent(state.agentID, task, state.error))
		} else {
			s.events.Emit(output.NewDelegationCompleteEvent(
				state.agentID,
				state.status,
				state.turnCount,
				state.tokenCount,
				state.toolCallCount,
				state.output,
			))
		}
		delete(pendingDelegates, msg.ToolCallID)
	} else if _, ok := startedToolCalls[msg.ToolCallID]; ok {
		s.events.Emit(output.NewToolCallFinishedEvent(0, msg.Name, msg.ToolCallID, msg.Content, nil))
	}
}
