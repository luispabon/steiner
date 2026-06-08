package output

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWithAgentScope(t *testing.T) {
	base := Event{
		Type:      "test_event",
		Timestamp: time.Unix(123, 0).UTC(),
		Payload:   map[string]any{"message": "hello"},
	}
	baseScoped := base
	baseScoped.Scope.AgentID = "parent-child"

	tests := []struct {
		name    string
		event   Event
		agentID string
		want    Event
	}{
		{
			name:    "empty agent id returns original event",
			event:   base,
			agentID: "",
			want:    base,
		},
		{
			name:    "non-empty agent id sets scope",
			event:   base,
			agentID: "child-1",
			want: func() Event {
				event := base
				event.Scope.AgentID = "child-1"
				return event
			}(),
		},
		{
			name:    "empty agent id preserves existing scope",
			event:   baseScoped,
			agentID: "",
			want:    baseScoped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithAgentScope(tt.event, tt.agentID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("WithAgentScope() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEventMarshalJSONScopes(t *testing.T) {
	base := Event{
		Type:      "test_event",
		Timestamp: time.Unix(123, 0).UTC(),
		Payload:   map[string]any{"message": "hello"},
	}
	scoped := WithAgentScope(base, "child-1")

	tests := []struct {
		name           string
		event          Event
		wantContains   string
		wantNotContain string
		wantScope      string
	}{
		{
			name:           "empty scope omitted",
			event:          base,
			wantNotContain: `"scope"`,
		},
		{
			name:         "agent scope included",
			event:        scoped,
			wantContains: `"scope":{"agent_id":"child-1"}`,
			wantScope:    "child-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			got := string(data)
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Fatalf("json = %s, want to contain %s", got, tt.wantContains)
			}
			if tt.wantNotContain != "" && strings.Contains(got, tt.wantNotContain) {
				t.Fatalf("json = %s, want not to contain %s", got, tt.wantNotContain)
			}

			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if got := roundTrip.Scope.AgentID; got != tt.wantScope {
				t.Fatalf("roundTrip.Scope.AgentID = %q, want %q", got, tt.wantScope)
			}
		})
	}
}

func TestNewModelCallFinishedEventTimingFields(t *testing.T) {
	cases := []struct {
		name       string
		durationMs int64
		ttftMs     int64
		outputTPS  float64
	}{
		{"zeros", 0, 0, 0},
		{"values", 1200, 340, 42.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := NewModelCallFinishedEvent(1, "model", "stop", 0, 100, nil, tc.durationMs, tc.ttftMs, tc.outputTPS)
			payload := ev.Payload.(ModelCallFinishedEvent)
			if payload.DurationMs != tc.durationMs {
				t.Errorf("DurationMs: got %d, want %d", payload.DurationMs, tc.durationMs)
			}
			if payload.TTFTMs != tc.ttftMs {
				t.Errorf("TTFTMs: got %d, want %d", payload.TTFTMs, tc.ttftMs)
			}
			if payload.OutputTPS != tc.outputTPS {
				t.Errorf("OutputTPS: got %f, want %f", payload.OutputTPS, tc.outputTPS)
			}
		})
	}
}
