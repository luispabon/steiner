package agent

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

func TestEmitRequestTokenDiagnosticIncludesCompactionThresholdTokens(t *testing.T) {
	var events []output.Event
	fit := prompt.RequestTokenBudget{
		EstimatedPromptTokens:     4200,
		RawEstimatedPromptTokens:  4100,
		PromptUsage:               0.42,
		CompactionThreshold:       0.70,
		CompactionThresholdTokens: 4200,
		SafetyMarginTokens:        256,
		TotalTokens:               4456,
		ContextSize:               10000,
		Fits:                      true,
	}

	emitRequestTokenDiagnostic(output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}), 3, fit, false)

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	payload, ok := events[0].Payload.(output.ContextBudgetEvent)
	if !ok {
		t.Fatalf("payload type = %T, want ContextBudgetEvent", events[0].Payload)
	}
	if payload.CompactionThreshold != 70 {
		t.Fatalf("CompactionThreshold = %v, want 70", payload.CompactionThreshold)
	}
	if payload.CompactionThresholdTokens != 4200 {
		t.Fatalf("CompactionThresholdTokens = %d, want 4200", payload.CompactionThresholdTokens)
	}
	foundThresholdNote := false
	for _, note := range payload.Notes {
		if strings.Contains(note, "compaction_threshold_tokens=4200") {
			foundThresholdNote = true
			break
		}
	}
	if !foundThresholdNote {
		t.Fatal("diagnostic notes missing absolute compaction threshold")
	}
}
