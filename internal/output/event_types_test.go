package output

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProviderDiagnosticEventIncludesTimingFields(t *testing.T) {
	data, err := json.Marshal(NewProviderDiagnosticEvent(ProviderDiagnosticEvent{
		TTFTMillis:     12,
		DurationMillis: 34,
	}))
	if err != nil {
		t.Fatalf("marshal provider diagnostic event: %v", err)
	}
	for _, want := range []string{`"ttft_millis":12`, `"duration_millis":34`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("event JSON = %s, missing %s", data, want)
		}
	}
}

func TestSkillEventJSONFieldNames(t *testing.T) {
	truncated, err := json.Marshal(NewSkillTruncatedEvent("docs", 2048, 1024))
	if err != nil {
		t.Fatalf("marshal skill_truncated event: %v", err)
	}
	truncatedText := string(truncated)
	for _, want := range []string{`"type":"skill_truncated"`, `"name":"docs"`, `"size_bytes":2048`, `"cap_bytes":1024`} {
		if !strings.Contains(truncatedText, want) {
			t.Errorf("skill_truncated JSON = %s, missing %s", truncated, want)
		}
	}

	state, err := json.Marshal(NewSkillStateEvent("docs", SkillStateDisabled))
	if err != nil {
		t.Fatalf("marshal skill_state event: %v", err)
	}
	stateText := string(state)
	for _, want := range []string{`"type":"skill_state"`, `"name":"docs"`, `"state":"disabled"`} {
		if !strings.Contains(stateText, want) {
			t.Errorf("skill_state JSON = %s, missing %s", state, want)
		}
	}
}

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
	event := NewDelegationStartedEvent(DelegationOccurrence{CallID: "call-1", AgentID: "child-1"}, "inspect", "model-a", "code")
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

func TestDelegationLifecycleEventsFlattenOccurrenceJSON(t *testing.T) {
	occ := DelegationOccurrence{CallID: "call-1", BatchID: "batch-1", AgentID: "agent-1"}
	tests := []struct {
		name  string
		event Event
	}{
		{"accepted", NewDelegationAcceptedEvent(occ, "g")},
		{"queued", NewDelegationQueuedEvent(occ, "code", "task")},
		{"started", NewDelegationStartedEvent(occ, "task", "alias", "code")},
		{"cache waiting", NewDelegationCacheWaitingEvent(occ, time.Unix(1, 0))},
		{"complete", NewDelegationCompleteEvent(DelegationCompleteParams{DelegationOccurrence: occ, Status: "complete"})},
		{"failed", NewDelegationFailedEvent(DelegationFailedParams{DelegationOccurrence: occ, Error: "boom"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Payload map[string]any `json:"payload"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"call_id": "call-1", "batch_id": "batch-1", "agent_id": "agent-1"} {
				if got, _ := decoded.Payload[key].(string); got != want {
					t.Errorf("payload[%q] = %v, want %q (JSON must stay flat: %s)", key, decoded.Payload[key], want, data)
				}
			}
			if _, nested := decoded.Payload["DelegationOccurrence"]; nested {
				t.Errorf("occurrence must be flattened, got %s", data)
			}
		})
	}
}

func TestDeliveredSubAgentKeepsParentCallIDKeyAndAddsBatchID(t *testing.T) {
	data, err := json.Marshal(NewSubAgentsDeliveredEvent([]DeliveredSubAgent{{AgentID: "a", Status: "complete", ParentCallID: "c", BatchID: "b"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"parent_call_id":"c"`, `"batch_id":"b"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("event JSON = %s, missing %s", data, want)
		}
	}
}

func TestDelegationOccurrenceOmitsEmptyCallAndBatch(t *testing.T) {
	data, err := json.Marshal(NewDelegationStartedEvent(DelegationOccurrence{AgentID: "a"}, "task", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "call_id") || strings.Contains(string(data), "batch_id") || !strings.Contains(string(data), `"agent_id":"a"`) {
		t.Errorf("event JSON = %s, want agent_id only", data)
	}
}

func TestSteerReceivedEventIncludesImages(t *testing.T) {
	event := NewSteerReceivedEvent("see [Image 1]", []ImageBlock{{
		ID:        "img-1",
		FilePath:  "/tmp/shot.png",
		MediaType: "image/png",
		Width:     10,
		Height:    20,
		SizeBytes: 128,
	}})
	payload, ok := event.Payload.(SteerReceivedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want SteerReceivedEvent", event.Payload)
	}
	if payload.Text != "see [Image 1]" {
		t.Errorf("Text = %q, want %q", payload.Text, "see [Image 1]")
	}
	if len(payload.Images) != 1 {
		t.Fatalf("len(Images) = %d, want 1", len(payload.Images))
	}
	if payload.Images[0].FilePath != "/tmp/shot.png" {
		t.Errorf("Images[0].FilePath = %q, want %q", payload.Images[0].FilePath, "/tmp/shot.png")
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	for _, want := range []string{`"type":"steer_received"`, `"text":"see [Image 1]"`, `"file_path":"/tmp/shot.png"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("event JSON = %s, missing %s", data, want)
		}
	}
}
