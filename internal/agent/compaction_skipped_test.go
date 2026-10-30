package agent

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestHandleCompactionNotAppliedEmitsSkippedTerminalEvent proves a compaction
// attempt that applies nothing ends with a skipped terminal event after the
// compacting started event, so consumers clear the compacting state.
func TestHandleCompactionNotAppliedEmitsSkippedTerminalEvent(t *testing.T) {
	prov := newClaudeLikeCompactionProvider(false)
	// Two messages are all retained turns, so the stage has no source and does not apply.
	conv := compactionTestTurns()[:2]
	var rec eventRecorder
	req := statefulRunRequest(prov, nil)
	req.SourceConversation = conv
	req.Events = rec.sink()
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, (&Runner{}).compactConversationForBudget)
	p.lastUsage = &provider.UsageStats{PromptTokens: 9500}
	fit := prompt.RequestTokenBudget{Fits: true, ContextSize: 10000}

	if outcome := p.handleCompaction(context.Background(), state, fit); outcome.Error != nil || outcome.Stop {
		t.Fatalf("handleCompaction() = %+v, want no error or stop while the request fits", outcome)
	}

	var severities []string
	var skipped output.ContextCompactionEvent
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeContextDiagnostics || output.ContextDiagnosticKind(event.Payload) != "compaction" {
			continue
		}
		payload, ok := output.AsContextCompactionEvent(event.Payload)
		if !ok {
			t.Fatalf("compaction payload type = %T, want output.ContextCompactionEvent", event.Payload)
		}
		severities = append(severities, payload.Severity)
		if payload.Severity == "skipped" {
			skipped = payload
		}
	}

	if want := []string{"compacting", "skipped"}; !slices.Equal(severities, want) {
		t.Fatalf("compaction severities = %v, want %v", severities, want)
	}
	if skipped.CompactedTurns != 0 || skipped.CompactedMessages != 0 {
		t.Errorf("skipped payload compacted turns=%d messages=%d, want 0", skipped.CompactedTurns, skipped.CompactedMessages)
	}
	if skipped.Turn != 1 {
		t.Errorf("skipped payload turn = %d, want 1", skipped.Turn)
	}
}

// TestHandleCompactionSkippedReportsFallbackReason proves a fallback that fails
// while main has nothing to compact reports the discarded fallback on the skipped
// terminal event.
func TestHandleCompactionSkippedReportsFallbackReason(t *testing.T) {
	prov := newClaudeLikeCompactionProvider(false)
	// Two messages are all retained turns, so the main stage has no source and does not apply.
	conv := compactionTestTurns()[:2]
	var rec eventRecorder
	req := statefulRunRequest(prov, nil)
	req.SourceConversation = conv
	req.Events = rec.sink()
	req.CompactionFallback = CompactionFallback{
		After: time.Minute,
		Ref:   "anthropic/claude-small",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{}, errors.New("boom")
		},
	}
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, (&Runner{}).compactConversationForBudget)
	p.lastUsage = &provider.UsageStats{PromptTokens: 9500}
	fit := prompt.RequestTokenBudget{Fits: true, ContextSize: 10000}

	if outcome := p.handleCompaction(context.Background(), state, fit); outcome.Error != nil || outcome.Stop {
		t.Fatalf("handleCompaction() = %+v, want no error or stop while the request fits", outcome)
	}

	var skipped output.ContextCompactionEvent
	found := false
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeContextDiagnostics || output.ContextDiagnosticKind(event.Payload) != "compaction" {
			continue
		}
		payload, ok := output.AsContextCompactionEvent(event.Payload)
		if !ok {
			t.Fatalf("compaction payload type = %T, want output.ContextCompactionEvent", event.Payload)
		}
		if payload.Severity == "skipped" {
			skipped = payload
			found = true
		}
	}
	if !found {
		t.Fatal("no skipped compaction event emitted")
	}
	if skipped.FallbackStatus != output.CompactionFallbackSkipped {
		t.Errorf("FallbackStatus = %q, want %q", skipped.FallbackStatus, output.CompactionFallbackSkipped)
	}
	if want := "fallback failed: resolve compaction fallback: boom"; skipped.FallbackReason != want {
		t.Errorf("FallbackReason = %q, want %q", skipped.FallbackReason, want)
	}
}
