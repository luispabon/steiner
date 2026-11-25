package output

import (
	"encoding/json"
	"reflect"
	"strings"
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
		CacheReadTokens:           11,
		InputTokens:               22,
		CacheCreateTokens:         33,
		CompactionThresholdTokens: 3200,
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
		Kind:                      "compaction",
		CacheReadTokens:           want.CacheReadTokens,
		InputTokens:               want.InputTokens,
		CacheCreateTokens:         want.CacheCreateTokens,
		CompactionThresholdTokens: want.CompactionThresholdTokens,
	}
	got, ok = AsContextCompactionEvent(legacy)
	if !ok {
		t.Fatal("AsContextCompactionEvent() ok = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AsContextCompactionEvent() = %+v, want %+v", got, want)
	}
}

func TestAsContextCompactionEventFallbackFields(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{name: "used", status: CompactionFallbackUsed},
		{name: "skipped", status: CompactionFallbackSkipped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy := ContextDiagnosticsEvent{
				Kind:                 "compaction",
				CompactionModel:      "claude-haiku-5-5",
				CompactionModelAlias: "haiku",
				FallbackStatus:       tt.status,
				FallbackReason:       "primary over budget",
			}
			event, ok := AsContextCompactionEvent(legacy)
			if !ok {
				t.Fatal("AsContextCompactionEvent() ok = false, want true")
			}
			if event.CompactionModel != legacy.CompactionModel || event.CompactionModelAlias != legacy.CompactionModelAlias || event.FallbackStatus != legacy.FallbackStatus || event.FallbackReason != legacy.FallbackReason {
				t.Fatalf("AsContextCompactionEvent() = %+v, want fallback fields from %+v", event, legacy)
			}
			if got := event.toLegacyContextDiagnostics(); !reflect.DeepEqual(got, legacy) {
				t.Fatalf("legacy round trip = %+v, want %+v", got, legacy)
			}
		})
	}
}

func TestAsContextCompactionEventFailureFields(t *testing.T) {
	legacy := ContextDiagnosticsEvent{
		Kind:                  "compaction",
		Severity:              "skipped",
		CompactionResult:      CompactionResultFailed,
		SummaryAttempts:       3,
		SummaryFailureReason:  "tool_calls",
		SummaryToolCalls:      2,
		SummaryAttemptDetails: []CompactionSummaryAttempt{{Attempt: 1, FinishReason: "stop", CompletionTokens: summaryTokens(0), ReasoningContent: true, Reason: "no_text"}},
	}
	event, ok := AsContextCompactionEvent(legacy)
	if !ok {
		t.Fatal("AsContextCompactionEvent() ok = false, want true")
	}
	if event.CompactionResult != legacy.CompactionResult || event.SummaryAttempts != legacy.SummaryAttempts || event.SummaryFailureReason != legacy.SummaryFailureReason || event.SummaryToolCalls != legacy.SummaryToolCalls || !reflect.DeepEqual(event.SummaryAttemptDetails, legacy.SummaryAttemptDetails) {
		t.Fatalf("AsContextCompactionEvent() = %+v, want failure fields from %+v", event, legacy)
	}
	if got := event.toLegacyContextDiagnostics(); !reflect.DeepEqual(got, legacy) {
		t.Fatalf("legacy round trip = %+v, want %+v", got, legacy)
	}
}

func TestAsContextCompactionEventInstrumentationFields(t *testing.T) {
	typed := ContextCompactionEvent{
		Severity:           "done",
		SummaryTruncated:   true,
		SummaryCondensed:   true,
		ProviderErrorClass: "http_4xx",
		HTTPStatus:         429,
	}

	legacy := typed.toLegacyContextDiagnostics()
	if legacy.SummaryTruncated != typed.SummaryTruncated || legacy.SummaryCondensed != typed.SummaryCondensed || legacy.ProviderErrorClass != typed.ProviderErrorClass || legacy.HTTPStatus != typed.HTTPStatus {
		t.Fatalf("legacy conversion = %+v, want instrumentation fields from %+v", legacy, typed)
	}

	back, ok := AsContextCompactionEvent(legacy)
	if !ok {
		t.Fatal("AsContextCompactionEvent() ok = false, want true")
	}
	if got := back.toLegacyContextDiagnostics(); !reflect.DeepEqual(got, legacy) {
		t.Fatalf("legacy round trip = %+v, want %+v", got, legacy)
	}

	legacyIn := ContextDiagnosticsEvent{
		Kind:               "compaction",
		Severity:           "failed",
		SummaryTruncated:   true,
		SummaryCondensed:   true,
		ProviderErrorClass: "timeout",
		HTTPStatus:         504,
	}
	event, ok := AsContextCompactionEvent(legacyIn)
	if !ok {
		t.Fatal("AsContextCompactionEvent() ok = false, want true")
	}
	if event.SummaryTruncated != legacyIn.SummaryTruncated || event.SummaryCondensed != legacyIn.SummaryCondensed || event.ProviderErrorClass != legacyIn.ProviderErrorClass || event.HTTPStatus != legacyIn.HTTPStatus {
		t.Fatalf("AsContextCompactionEvent() = %+v, want instrumentation fields from %+v", event, legacyIn)
	}
	if got := event.toLegacyContextDiagnostics(); !reflect.DeepEqual(got, legacyIn) {
		t.Fatalf("legacy round trip = %+v, want %+v", got, legacyIn)
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
				CompactionCount: 2, RestartGuidance: "guidance", SummarizedMessages: 5,
				RetainedMessages: 4, RetainedUnits: 3, RetainedUserMessages: 2, RetainedSubAgentResults: 1,
				SummaryTitle: "title", SummaryPreview: "preview", SummaryText: "text",
				SummaryBytes: 7, CacheReadTokens: 8, InputTokens: 9, CacheCreateTokens: 10,
				PromptTokens: 11, RawPromptTokens: 12, ContextTokens: 13, TotalTokens: 14,
				Truncated: true, ContextWindow: 15, ContextUsagePercent: 16.5,
				CompactionThreshold: 17.5, CompactionThresholdTokens: 18, EstimatorPadTokens: 18, Status: "status",
				Mode: "mode", BeforePromptTokens: 19, BeforeRawPromptTokens: 20,
				BeforeUsagePercent: 21.5, AfterPromptTokens: 22, AfterRawPromptTokens: 23,
				AfterUsagePercent: 24.5, SummaryTokenBudget: 26,
				CompactionModel: "model", CompactionModelAlias: "alias", FallbackStatus: CompactionFallbackUsed, FallbackReason: "reason",
				ThresholdAchieved: true, Notes: []string{"note"},
				Stages: []ContextCompactionStage{{Stage: "normal", RetainedUnits: 2, SourceTokens: 3, SummaryTokens: 4, RetainedTokens: 5, ProtectedTokens: 6, Failed: true}},
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

func TestContextCompactionStagesLegacyRoundTrip(t *testing.T) {
	stages := []ContextCompactionStage{
		{Stage: "normal", RetainedUnits: 3, SourceTokens: 120, SummaryTokens: 40, RetainedTokens: 300, ProtectedTokens: 20},
		{Stage: "emergency", RetainedUnits: 1, RetainedTokens: 7, ProtectedTokens: 3, Failed: true},
	}
	tests := []struct {
		name   string
		legacy ContextDiagnosticsEvent
		want   []ContextCompactionStage
	}{
		{name: "stages", legacy: ContextDiagnosticsEvent{Kind: "compaction", Stages: stages}, want: stages},
		{name: "no stages stay nil", legacy: ContextDiagnosticsEvent{Kind: "compaction"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, ok := AsContextCompactionEvent(tt.legacy)
			if !ok {
				t.Fatal("AsContextCompactionEvent() ok = false, want true")
			}
			if !reflect.DeepEqual(event.Stages, tt.want) {
				t.Fatalf("Stages = %+v, want %+v", event.Stages, tt.want)
			}
			if got := event.toLegacyContextDiagnostics(); !reflect.DeepEqual(got, tt.legacy) {
				t.Fatalf("legacy round trip = %+v, want %+v", got, tt.legacy)
			}
		})
	}
}

func TestContextCompactionStagesAreCopied(t *testing.T) {
	typed := ContextCompactionEvent{Stages: []ContextCompactionStage{{Stage: "normal"}}}
	clone, ok := CloneContextDiagnosticPayload(typed)
	if !ok {
		t.Fatal("CloneContextDiagnosticPayload() ok = false, want true")
	}
	typed.Stages[0].Stage = "changed"
	if got := clone.(ContextCompactionEvent).Stages[0].Stage; got != "normal" {
		t.Fatalf("cloned stage = %q, want %q: clone shares the stages slice", got, "normal")
	}
}

func TestContextCompactionStagesJSON(t *testing.T) {
	tests := []struct {
		name    string
		stages  []ContextCompactionStage
		omitted bool
		want    string
	}{
		{
			name:   "failed emergency stage",
			stages: []ContextCompactionStage{{Stage: "emergency", RetainedUnits: 1, RetainedTokens: 7, ProtectedTokens: 3, Failed: true}},
			want:   `"stages":[{"stage":"emergency","retained_units":1,"retained_tokens":7,"protected_tokens":3,"failed":true}]`,
		},
		{name: "no stages omit the key", omitted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(contextDiagnosticPayloadForJSON(ContextCompactionEvent{Stages: tt.stages}))
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if tt.omitted {
				if strings.Contains(string(data), `"stages"`) {
					t.Errorf("json = %s, want no stages key", data)
				}
				return
			}
			if !strings.Contains(string(data), tt.want) {
				t.Errorf("json = %s, want it to contain %s", data, tt.want)
			}
		})
	}
}
