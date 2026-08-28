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
	}
	if !reflect.DeepEqual(budget, want) {
		t.Fatalf("AsContextBudgetEvent() = %+v, want %+v", budget, want)
	}
}
