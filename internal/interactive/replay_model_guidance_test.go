package interactive

import (
	"encoding/json"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// TestReplayKeepsModelGuidanceAcrossPersistence checks a resumed session
// replays a model-guidance rejection with its flag, so the TUI stays as quiet
// as it was live.
func TestReplayKeepsModelGuidanceAcrossPersistence(t *testing.T) {
	t.Parallel()
	live := []agent.Message{
		delegateCall("call", "follow_up", "continue"),
		{Role: agent.MessageRoleTool, ToolCallID: "call", Name: "follow_up", Content: `{"ok":false,"error":{"message":"worktree gone; delegate a fresh code agent"}}`, DelegationAdmission: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: "batch", ModelGuidance: true}},
	}
	data, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	var persisted []agent.Message
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	finished := eventsOfType(replayEvents(t, persisted), output.EventTypeToolCallFinished)
	if len(finished) != 1 {
		t.Fatalf("finished events = %d, want 1", len(finished))
	}
	p := finished[0].Payload.(output.ToolCallFinishedEvent)
	if p.DelegationAdmission == nil || !p.DelegationAdmission.ModelGuidance || p.DelegationAdmission.Status != tool.DelegationAdmissionRejected {
		t.Fatalf("replayed admission = %#v, want rejected model guidance", p.DelegationAdmission)
	}
}
