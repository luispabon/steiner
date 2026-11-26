package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

// TestNewSegmentStageUnmappedRetentionKeepsRebaseReason covers the defensive
// rebase an unmapped retention boundary forces: on a valid chain the boundary
// reads as a split at the chain start, so that decision alone is relabelled
// retention-unmapped, while every other rebase keeps its own reason.
func TestNewSegmentStageUnmappedRetentionKeepsRebaseReason(t *testing.T) {
	full := segmentStageConversation()
	retained := full[4:9]
	if _, mapped := segmentRetentionStart(full, retained); mapped {
		t.Fatal("retention mapped, want an unmapped boundary")
	}
	req := segmentedCompactionRequest(oneSummaryProvider(), segmentedCompactionPolicy())
	tests := []struct {
		name  string
		mode  prompt.CompactionMode
		retry bool
		want  string
	}{
		{name: "split in chain", mode: prompt.CompactionModeNormal, want: segmentReasonRetentionUnmapped},
		{name: "emergency", mode: prompt.CompactionModeEmergency, want: segmentReasonEmergency},
		{name: "retry", mode: prompt.CompactionModeNormal, retry: true, want: segmentReasonRetry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, err := newSegmentStage(context.Background(), req, stateFromMessages(full), segmentStageInput{
				Policy:    req.SegmentedCompaction,
				Full:      full,
				Source:    full[:5],
				Retained:  retained,
				Mode:      tt.mode,
				Model:     "test-model",
				Budget:    prompt.RequestTokenBudget{ContextSize: 100000, CompactionThresholdTokens: 70000},
				MaxTokens: 64,
				Retry:     tt.retry,
			})
			if err != nil {
				t.Fatalf("newSegmentStage() error = %v", err)
			}
			if !stage.Decision.Rebase || stage.Decision.Reason != tt.want {
				t.Fatalf("decision = %+v, want the %s rebase reason", stage.Decision, tt.want)
			}
		})
	}
}
