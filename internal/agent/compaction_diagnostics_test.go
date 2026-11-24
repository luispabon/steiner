package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
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

// compactionDiagnosticsRun runs one compaction and returns the finished compaction
// diagnostics it emitted, with the compaction error.
func compactionDiagnosticsRun(t *testing.T, req RunRequest, state RunState) ([]output.ContextCompactionEvent, error) {
	t.Helper()
	var rec eventRecorder
	req.Events = rec.sink()
	compactionCount := 0
	_, _, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 0, nil, map[string]bool{}, &compactionCount, "")
	var finished []output.ContextCompactionEvent
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeContextDiagnostics || output.ContextDiagnosticKind(event.Payload) != "compaction" {
			continue
		}
		payload, ok := output.AsContextCompactionEvent(event.Payload)
		if !ok {
			t.Fatalf("compaction payload type = %T, want output.ContextCompactionEvent", event.Payload)
		}
		if payload.Severity != "compacting" {
			finished = append(finished, payload)
		}
	}
	return finished, err
}

// TestCompactionEventReportsStageSizes proves an applied compaction emits one stage
// entry per stage that applied, each sized from its own summary and tail.
func TestCompactionEventReportsStageSizes(t *testing.T) {
	detail := []Message{
		{Role: MessageRoleUser, Content: strings.Repeat("turn 1 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 1 assistant detail ", 4)},
		{Role: MessageRoleUser, Content: strings.Repeat("turn 2 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 2 assistant detail ", 4)},
		{Role: MessageRoleUser, Content: strings.Repeat("turn 3 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 3 assistant detail ", 4)},
		{Role: MessageRoleUser, Content: strings.Repeat("turn 4 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 4 assistant detail ", 4)},
	}
	tests := []struct {
		name       string
		state      RunState
		req        RunRequest
		responses  []provider.ChatResponse
		wantStages []string
		wantUnits  map[string]int
	}{
		{
			name:       "normal stage",
			state:      RunState{Conversation: retentionFixtureMessages(), Lineage: newConversationLineage(retentionFixtureMessages())},
			req:        retentionFixtureRequest(5060, nil),
			responses:  []provider.ChatResponse{{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary handoff text"}, FinishReason: "stop"}},
			wantStages: []string{"normal"},
			wantUnits:  map[string]int{"normal": 2},
		},
		{
			name:  "normal then emergency stage",
			state: RunState{Conversation: detail, Lineage: newConversationLineage(detail)},
			req: RunRequest{
				ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
				ModelBudget: prompt.ModelTokenBudget{
					ContextSize:               7500,
					MaxCompletionTokens:       128,
					NormalSummaryMaxTokens:    64,
					EmergencySummaryMaxTokens: 16,
				},
			},
			responses: []provider.ChatResponse{
				{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: strings.Repeat("normal summary detail ", 1200)}, FinishReason: "stop"},
				{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "emergency handoff summary"}, FinishReason: "stop"},
			},
			wantStages: []string{"normal", "emergency"},
			wantUnits:  map[string]int{"emergency": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req.Provider = &fakeProvider{responses: tt.responses}
			events, err := compactionDiagnosticsRun(t, tt.req, tt.state)
			if err != nil {
				t.Fatalf("compaction error = %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("finished compaction diagnostics = %d, want 1", len(events))
			}
			stages := events[0].Stages
			if len(stages) != len(tt.wantStages) {
				t.Fatalf("Stages = %+v, want stages %v", stages, tt.wantStages)
			}
			for i, stage := range stages {
				if stage.Stage != tt.wantStages[i] {
					t.Errorf("stage %d = %q, want %q", i, stage.Stage, tt.wantStages[i])
				}
				if stage.Failed {
					t.Errorf("stage %s Failed = true, want false for an applied stage", stage.Stage)
				}
				if stage.SourceTokens <= 0 || stage.SummaryTokens <= 0 || stage.RetainedTokens <= 0 {
					t.Errorf("stage %s sizes = %+v, want non-zero source, summary and retained tokens", stage.Stage, stage)
				}
				if want, ok := tt.wantUnits[stage.Stage]; ok && stage.RetainedUnits != want {
					t.Errorf("stage %s RetainedUnits = %d, want %d", stage.Stage, stage.RetainedUnits, want)
				}
			}
		})
	}
}

// TestCompactionFailureReportsTailBreakdown proves a newest unit too large for the
// main budget emits one failed emergency stage with the breakdown, and no summary runs.
func TestCompactionFailureReportsTailBreakdown(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 assistant", ToolCalls: []ToolCall{{ID: "call-2", Name: "read"}}},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-2", Content: retentionHugeOutput},
	}
	state := RunState{Conversation: messages, Lineage: newConversationLineage(messages)}
	providerStub := &fakeProvider{}

	events, err := compactionDiagnosticsRun(t, retentionFixtureRequest(3000, providerStub), state)
	var tooLarge *compactionTailTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("compaction error = %v, want compactionTailTooLargeError", err)
	}
	if tooLarge.RetainedTokens <= 0 || tooLarge.ProtectedTokens <= 0 {
		t.Fatalf("breakdown = %+v, want non-zero retained and protected tokens", *tooLarge)
	}
	if got := len(providerStub.requests); got != 0 {
		t.Errorf("provider calls = %d, want 0", got)
	}
	if len(events) != 1 {
		t.Fatalf("finished compaction diagnostics = %d, want 1", len(events))
	}
	want := []output.ContextCompactionStage{{
		Stage:           "emergency",
		RetainedUnits:   1,
		RetainedTokens:  tooLarge.RetainedTokens,
		ProtectedTokens: tooLarge.ProtectedTokens,
		Failed:          true,
	}}
	if !slices.Equal(events[0].Stages, want) {
		t.Errorf("Stages = %+v, want %+v", events[0].Stages, want)
	}
}

// TestEmitCompactionFailedDiagnostics proves only a too-large tail error emits the
// failed stage, after the stages that already applied.
func TestEmitCompactionFailedDiagnostics(t *testing.T) {
	tooLarge := &compactionTailTooLargeError{RetainedTokens: 7, ProtectedTokens: 3}
	applied := output.ContextCompactionStage{Stage: "normal", RetainedUnits: 2, SourceTokens: 40, SummaryTokens: 9, RetainedTokens: 30, ProtectedTokens: 5}
	failed := output.ContextCompactionStage{Stage: "emergency", RetainedUnits: 1, RetainedTokens: 7, ProtectedTokens: 3, Failed: true}
	tests := []struct {
		name       string
		err        error
		prior      []output.ContextCompactionStage
		wantEvent  bool
		wantStages []output.ContextCompactionStage
	}{
		{name: "tail too large after an applied stage", err: tooLarge, prior: []output.ContextCompactionStage{applied}, wantEvent: true, wantStages: []output.ContextCompactionStage{applied, failed}},
		{name: "wrapped tail too large", err: fmt.Errorf("fallback: %w", tooLarge), wantEvent: true, wantStages: []output.ContextCompactionStage{failed}},
		{name: "other error emits nothing", err: errors.New("provider down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec eventRecorder
			emitCompactionFailedDiagnostics(rec.sink(), 4, tt.prior, tt.err)
			events := rec.snapshot()
			if !tt.wantEvent {
				if len(events) != 0 {
					t.Fatalf("events = %d, want none", len(events))
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("events = %d, want 1", len(events))
			}
			payload, ok := output.AsContextCompactionEvent(events[0].Payload)
			if !ok {
				t.Fatalf("payload type = %T, want output.ContextCompactionEvent", events[0].Payload)
			}
			if payload.Turn != 4 || payload.Severity != "skipped" {
				t.Errorf("Turn, Severity = %d, %q, want 4, skipped", payload.Turn, payload.Severity)
			}
			if !slices.Equal(payload.Stages, tt.wantStages) {
				t.Errorf("Stages = %+v, want %+v", payload.Stages, tt.wantStages)
			}
		})
	}
}
