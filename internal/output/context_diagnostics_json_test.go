package output

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestContextDiagnosticEventJSONPreservesLegacyKindAndFields(t *testing.T) {
	tests := []struct {
		name      string
		payload   contextDiagnosticPayload
		kind      string
		fieldText string
	}{
		{
			name:      "compaction",
			payload:   contextDiagnosticFromLegacy(ContextDiagnosticsEvent{Kind: "compaction", Turn: 7, SummaryBytes: 128, CompactionThresholdTokens: 3000, Notes: []string{"note"}}),
			kind:      "compaction",
			fieldText: `"compaction_threshold_tokens":3000`,
		},
		{
			name:      "session health",
			payload:   ContextSessionHealthEvent{Turn: 8, CompactionCount: 2, Notes: []string{"note"}},
			kind:      "session_health",
			fieldText: `"compaction_count":2`,
		},
		{
			name:      "budget",
			payload:   ContextBudgetEvent{Turn: 9, ContextTokens: 4096, CompactionThresholdTokens: 3000, Status: "ok"},
			kind:      "budget",
			fieldText: `"compaction_threshold_tokens":3000`,
		},
		{
			name:      "session loaded",
			payload:   contextDiagnosticFromLegacy(ContextDiagnosticsEvent{Kind: "session_loaded", ContextTokens: 8192}),
			kind:      "session_loaded",
			fieldText: `"context_tokens":8192`,
		},
		{
			name:      "file annotation",
			payload:   ContextFileAnnotationEvent{Turn: 10, Path: "note.txt"},
			kind:      "file_annotation",
			fieldText: `"path":"note.txt"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(Event{
				Type:      EventTypeContextDiagnostics,
				Timestamp: time.Unix(0, 0).UTC(),
				Payload:   tt.payload,
			})
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			text := string(data)
			if want := `"kind":"` + tt.kind + `"`; !strings.Contains(text, want) {
				t.Fatalf("event JSON = %s, missing %s", text, want)
			}
			if !strings.Contains(text, tt.fieldText) {
				t.Fatalf("event JSON = %s, missing %s", text, tt.fieldText)
			}
		})
	}
}

func TestContextCompactionFallbackFieldsJSON(t *testing.T) {
	typed := ContextCompactionEvent{
		CompactionModel: "claude-haiku-5-5",
		FallbackStatus:  CompactionFallbackSkipped,
		FallbackReason:  "no fallback configured",
	}
	data, err := json.Marshal(Event{
		Type:      EventTypeContextDiagnostics,
		Timestamp: time.Unix(0, 0).UTC(),
		Payload:   typed,
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"compaction_model":"claude-haiku-5-5"`, `"fallback_status":"skipped"`, `"fallback_reason":"no fallback configured"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("event JSON = %s, missing %s", text, want)
		}
	}

	var decoded struct {
		Payload ContextDiagnosticsEvent `json:"payload"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	got, ok := AsContextCompactionEvent(decoded.Payload)
	if !ok {
		t.Fatal("AsContextCompactionEvent() ok = false, want true")
	}
	if !reflect.DeepEqual(got, typed) {
		t.Fatalf("decoded compaction = %+v, want %+v", got, typed)
	}
}

func TestContextCompactionFallbackFieldsOmittedJSON(t *testing.T) {
	data, err := json.Marshal(Event{
		Type:      EventTypeContextDiagnostics,
		Timestamp: time.Unix(0, 0).UTC(),
		Payload:   ContextCompactionEvent{Turn: 2, CompactedTurns: 1},
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	text := string(data)
	for _, key := range []string{"compaction_model", "fallback_status", "fallback_reason"} {
		if strings.Contains(text, key) {
			t.Fatalf("event JSON = %s, must not contain %s when unset", text, key)
		}
	}
}

func TestScopedContextDiagnosticEventJSONPreservesLegacyKind(t *testing.T) {
	data, err := json.Marshal(WithAgentScope(NewContextBudgetEvent("conversation", 3, 0, 0, true), "child-1"))
	if err != nil {
		t.Fatalf("marshal scoped event: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"budget"`, `"scope":"conversation"`, `"truncated":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("scoped event JSON = %s, missing %s", text, want)
		}
	}
}
