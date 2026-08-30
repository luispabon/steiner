package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDelegationWorktreeDisposalEventJSON(t *testing.T) {
	event := WithAgentTypeScope(WithAgentScope(NewDelegationWorktreeDisposalEvent("child-1", false, "failed"), "child-1"), "code")
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	jsonText := string(data)
	for _, want := range []string{`"type":"delegation_worktree_disposal"`, `"agent_id":"child-1"`, `"removed":false`, `"error":"failed"`, `"agent_type":"code"`} {
		if !strings.Contains(jsonText, want) {
			t.Errorf("event JSON = %s, missing %s", jsonText, want)
		}
	}
}

func TestDelegationStartedEventIncludesAgentType(t *testing.T) {
	event := NewDelegationStartedEventWithType("child-1", "inspect", "call-1", "model-a", "code")
	payload, ok := event.Payload.(DelegationStartedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want DelegationStartedEvent", event.Payload)
	}
	if payload.AgentType != "code" {
		t.Errorf("AgentType = %q, want %q", payload.AgentType, "code")
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if !strings.Contains(string(data), `"agent_type":"code"`) {
		t.Errorf("event JSON = %s, want agent_type", data)
	}
}
