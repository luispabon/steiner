package output

import (
	"reflect"
	"testing"
)

func TestContextCompactionEventBudgetSnapshot(t *testing.T) {
	compaction := ContextCompactionEvent{
		Scope:               "run",
		Turn:                7,
		PromptTokens:        100,
		RawPromptTokens:     140,
		ContextTokens:       200,
		TotalTokens:         300,
		ContextWindow:       4000,
		ContextUsagePercent: 42.5,
		CompactionThreshold: 80,
		EstimatorPadTokens:  10,
		Status:              "ok",
		SummaryText:         "should not carry over",
	}

	snapshot := compaction.BudgetSnapshot()

	want := ContextBudgetEvent{
		Turn:                7,
		PromptTokens:        100,
		RawPromptTokens:     140,
		ContextTokens:       200,
		TotalTokens:         300,
		ContextWindow:       4000,
		ContextUsagePercent: 42.5,
		CompactionThreshold: 80,
		EstimatorPadTokens:  10,
		Status:              "ok",
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("BudgetSnapshot() = %+v, want %+v", snapshot, want)
	}
}

func TestContextCompactionEventUsageRoundTrip(t *testing.T) {
	want := ContextCompactionEvent{
		CacheReadTokens:   11,
		InputTokens:       22,
		CacheCreateTokens: 33,
	}

	legacy := want.toLegacyContextDiagnostics()
	got, ok := contextDiagnosticFromLegacy(legacy).(ContextCompactionEvent)
	if !ok {
		t.Fatal("contextDiagnosticFromLegacy() type assertion failed")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contextDiagnosticFromLegacy() = %+v, want %+v", got, want)
	}

	legacy = ContextDiagnosticsEvent{
		Kind:              "compaction",
		CacheReadTokens:   want.CacheReadTokens,
		InputTokens:       want.InputTokens,
		CacheCreateTokens: want.CacheCreateTokens,
	}
	got, ok = AsContextCompactionEvent(legacy)
	if !ok {
		t.Fatal("AsContextCompactionEvent() ok = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AsContextCompactionEvent() = %+v, want %+v", got, want)
	}
}

func TestAsContextBudgetEventSessionLoaded(t *testing.T) {
	payload := ContextDiagnosticsEvent{
		Kind:                "session_loaded",
		Turn:                3,
		PromptTokens:        150,
		ContextTokens:       8000,
		TotalTokens:         150,
		ContextWindow:       8000,
		ContextUsagePercent: 1.875,
		Status:              "ok",
	}

	budget, ok := AsContextBudgetEvent(payload)
	if !ok {
		t.Fatal("AsContextBudgetEvent() ok = false, want true")
	}

	want := ContextBudgetEvent{
		Turn:                3,
		PromptTokens:        150,
		ContextTokens:       8000,
		TotalTokens:         150,
		ContextWindow:       8000,
		ContextUsagePercent: 1.875,
		Status:              "ok",
		legacyKind:          "session_loaded",
	}
	if !reflect.DeepEqual(budget, want) {
		t.Fatalf("AsContextBudgetEvent() = %+v, want %+v", budget, want)
	}

	legacy := budget.toLegacyContextDiagnostics()
	if legacy.Kind != "session_loaded" {
		t.Fatalf("budget legacy kind = %q, want session_loaded", legacy.Kind)
	}
}

func TestContextDiagnosticTypedLegacyRoundTripsPreserveFields(t *testing.T) {
	tests := []struct {
		name    string
		payload contextDiagnosticPayload
	}{
		{
			name: "compaction",
			payload: ContextCompactionEvent{
				Scope: "scope", Turn: 1, Severity: "severity", SessionState: "state",
				CompactionCount: 2, RestartGuidance: "guidance", RetainedTurns: 3,
				RetainedMessages: 4, CompactedTurns: 5, CompactedMessages: 6,
				SummaryTitle: "title", SummaryPreview: "preview", SummaryText: "text",
				SummaryBytes: 7, CacheReadTokens: 8, InputTokens: 9, CacheCreateTokens: 10,
				PromptTokens: 11, RawPromptTokens: 12, ContextTokens: 13, TotalTokens: 14,
				Truncated: true, ContextWindow: 15, ContextUsagePercent: 16.5,
				CompactionThreshold: 17.5, EstimatorPadTokens: 18, Status: "status",
				Mode: "mode", BeforePromptTokens: 19, BeforeRawPromptTokens: 20,
				BeforeUsagePercent: 21.5, AfterPromptTokens: 22, AfterRawPromptTokens: 23,
				AfterUsagePercent: 24.5, RetainedRawTurns: 25, SummaryTokenBudget: 26,
				ThresholdAchieved: true, Notes: []string{"note"},
			},
		},
		{
			name: "session health",
			payload: ContextSessionHealthEvent{
				Scope: "scope", Turn: 1, Severity: "severity", SessionState: "state",
				CompactionCount: 2, RestartGuidance: "guidance", Notes: []string{"note"},
			},
		},
		{
			name: "budget",
			payload: ContextBudgetEvent{
				Scope: "scope", Turn: 1, UsedBytes: 2, BudgetBytes: 3,
				PromptTokens: 4, RawPromptTokens: 5, ContextTokens: 6, TotalTokens: 7,
				Truncated: true, ContextWindow: 8, ContextUsagePercent: 9.5,
				CompactionThreshold: 10.5, EstimatorPadTokens: 11, Status: "status",
				Notes: []string{"note"},
			},
		},
		{
			name: "file annotation",
			payload: ContextFileAnnotationEvent{
				Scope: "scope", Turn: 1, Severity: "severity", Action: "action",
				Reason: "reason", Path: "path", Notes: []string{"note"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy := tt.payload.toLegacyContextDiagnostics()
			got := contextDiagnosticFromLegacy(legacy)
			if !reflect.DeepEqual(got, tt.payload) {
				t.Fatalf("typed -> legacy -> typed = %#v, want %#v", got, tt.payload)
			}
			legacy.Notes[0] = "changed"
			if tt.payload.toLegacyContextDiagnostics().Notes[0] != "note" {
				t.Fatal("legacy conversion reused notes slice")
			}
		})
	}
}
