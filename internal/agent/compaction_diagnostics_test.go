package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// TestCompactConversationForBudgetReportsCompactionCounts proves the emitted
// compaction diagnostics report the messages actually summarized and describe
// the retained tail instead of counting whole-candidate turns.
func TestCompactConversationForBudgetReportsCompactionCounts(t *testing.T) {
	tail := []Message{
		{Role: MessageRoleUser, Content: "old question"},
		{Role: MessageRoleAssistant, Content: "old answer"},
		{Role: MessageRoleUser, Content: "middle question"},
		{Role: MessageRoleAssistant, Content: "middle answer"},
		{Role: MessageRoleUser, Content: "recent question"},
		{Role: MessageRoleAssistant, Content: "recent answer"},
		{Role: MessageRoleTool, Content: "recent tool output"},
		{Role: MessageRoleUser, Source: MessageSourceSubAgentResult, Content: "sub-agent result"},
		{Role: MessageRoleAssistant, Content: "after the sub-agent"},
		{Role: MessageRoleUser, Content: "latest question"},
		{Role: MessageRoleAssistant, Content: "latest answer"},
	}
	generation := newConversationGeneration(1, []Message{{Role: MessageRoleSummary, Content: "prior summary"}}, tail)
	lineage := ConversationLineage{Generations: []ConversationGeneration{generation}, NextGenerationID: 2}
	conv := generation.FullMessages()
	state := RunState{Conversation: conv, Lineage: lineage}
	prov := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false)}}
	var rec eventRecorder
	req := compactionTestBudget()
	req.Provider = prov
	req.Events = rec.sink()
	compactionCount := 0

	attempted, applied, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 0, nil, map[string]bool{}, &compactionCount, "")
	if err != nil {
		t.Fatalf("compactConversationForBudgetWithSteering() error = %v", err)
	}
	if !attempted || !applied {
		t.Fatalf("attempted = %t, applied = %t, want both true", attempted, applied)
	}

	var payload output.ContextCompactionEvent
	found := false
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeContextDiagnostics || output.ContextDiagnosticKind(event.Payload) != "compaction" {
			continue
		}
		compaction, ok := output.AsContextCompactionEvent(event.Payload)
		if !ok {
			t.Fatalf("compaction payload type = %T, want output.ContextCompactionEvent", event.Payload)
		}
		if compaction.Severity == "compacting" {
			continue
		}
		payload = compaction
		found = true
	}
	if !found {
		t.Fatal("no finished compaction diagnostics emitted")
	}

	// Source is the prior summary plus the first two units ("old" and "middle"),
	// so only those four messages were summarized.
	if got, want := payload.SummarizedMessages, 4; got != want {
		t.Errorf("SummarizedMessages = %d, want %d", got, want)
	}
	if got, want := payload.RetainedMessages, 7; got != want {
		t.Errorf("RetainedMessages = %d, want %d", got, want)
	}
	if got, want := payload.RetainedUnits, 3; got != want {
		t.Errorf("RetainedUnits = %d, want %d", got, want)
	}
	if got, want := payload.RetainedUserMessages, 2; got != want {
		t.Errorf("RetainedUserMessages = %d, want %d", got, want)
	}
	if got, want := payload.RetainedSubAgentResults, 1; got != want {
		t.Errorf("RetainedSubAgentResults = %d, want %d", got, want)
	}
	if got, want := payload.RequestBlock, output.RequestBlockNew; got != want {
		t.Errorf("RequestBlock = %q, want %q", got, want)
	}
	if got, want := payload.RequestBlockBytes, len("middle question"); got != want {
		t.Errorf("RequestBlockBytes = %d, want %d", got, want)
	}
	if !slices.Contains(payload.Notes, "request_block=new") {
		t.Errorf("Notes = %v, want request_block=new", payload.Notes)
	}
	if !slices.Contains(payload.Notes, "retained_units=3") {
		t.Errorf("Notes = %v, want retained_units=3", payload.Notes)
	}
}
