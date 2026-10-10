package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

//nolint:gocyclo
func TestRunnerKeepsPromptBoundedAcrossToolTurns(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_3", Name: "read", Arguments: map[string]any{"path": "c.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "alpha"},
			}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 2000},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 4; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	if got, want := len(state.Lineage.Generations), 1; got != want {
		t.Fatalf("lineage generations = %d, want %d", got, want)
	}
	if got, want := len(state.Lineage.FullMessages()), len(state.Conversation); got != want {
		t.Fatalf("lineage/full conversation len = %d, want %d", got, want)
	}
	if got, want := state.Lineage.FullMessages()[0].Content, "start"; got != want {
		t.Fatalf("lineage first message = %q, want %q", got, want)
	}
}

func TestRunnerCompactionExhaustionFailsFastWithoutCallingProvider(t *testing.T) {
	providerStub := &fakeProvider{}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{Value: map[string]any{"contents": "unused"}}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               16,
			MaxCompletionTokens:       16,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          8,
			NormalSummaryMaxTokens:    8,
			EmergencySummaryMaxTokens: 8,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: strings.Repeat("latest user content ", 10)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("latest assistant content ", 10)},
			},
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 2000},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want irreducible compaction failure")
	}
	if !strings.Contains(err.Error(), "compaction cannot solve this request") {
		t.Fatalf("Run() error = %v, want compaction cannot solve this request", err)
	}
	if got, want := state.StopReason, StopReasonError; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 0; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
}

func TestRunnerEmitsContextDiagnosticsForBudgetPressureAndCompaction(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "alpha"},
			}, nil
		},
	}

	var events []output.Event
	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		Limits: Limits{MaxTurns: 6, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var kinds []string
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		kinds = append(kinds, output.ContextDiagnosticKind(event.Payload))
	}
	if !containsString(kinds, "budget") {
		t.Fatalf("diagnostic kinds = %v, want budget event", kinds)
	}
	if containsString(kinds, "compaction") {
		t.Fatalf("diagnostic kinds = %v, want no compaction event at this stage", kinds)
	}
}

//nolint:gocyclo
func TestRunnerRecompactsUntilTheBudgetFits(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "first handoff",
				},
				FinishReason: "stop",
				Usage: &provider.UsageStats{
					PromptTokens:             100,
					CacheReadInputTokens:     30,
					CacheCreationInputTokens: 20,
				},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "second handoff",
				},
				FinishReason: "stop",
				Usage: &provider.UsageStats{
					PromptTokens:             100,
					CacheReadInputTokens:     30,
					CacheCreationInputTokens: 20,
				},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage: &provider.UsageStats{
					PromptTokens:             100,
					CacheReadInputTokens:     30,
					CacheCreationInputTokens: 20,
				},
			},
		},
	}
	executor := &fakeExecutor{}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               3235, // Tuned to trigger exactly one compaction cycle; tracks preamble size growth
			MaxCompletionTokens:       32,
			SummaryMaxTokens:          32,
			NormalSummaryMaxTokens:    32,
			EmergencySummaryMaxTokens: 16,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: strings.Repeat("initial request ", 120)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("initial answer ", 96)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("second request ", 112)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("second answer ", 88)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("third request ", 96)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("third answer ", 80)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("follow up request ", 88)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("follow up answer ", 72)},
			},
		},
		Limits: Limits{MaxTurns: 3},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	// Assert the compaction invariant rather than an exact pass count: this
	// fixture's ContextSize budget tracks preamble size, so the number of
	// recompaction rounds is not a stable contract. At least one model request
	// and one compaction generation prove recompaction ran until the budget fit.
	if got := len(providerStub.requests); got < 2 {
		t.Fatalf("provider requests = %d, want at least one compaction and one model request", got)
	}
	if got := len(state.Lineage.Generations); got < 2 {
		t.Fatalf("lineage generations = %d, want at least one compaction generation", got)
	}
	if got := len(state.Lineage.Generations[1].SummaryPrefix); got == 0 {
		t.Fatal("latest summary prefix = empty, want retained compaction summary")
	}

	var compactionCount int
	var sessionHealthCount int
	var budgetCount int
	var compactionCounts []int
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		switch output.ContextDiagnosticKind(event.Payload) {
		case "compaction":
			payload, ok := output.AsContextCompactionEvent(event.Payload)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextCompactionEvent", event.Payload)
			}
			if payload.Severity == "compacting" {
				continue
			}
			compactionCount++
			compactionCounts = append(compactionCounts, payload.CompactionCount)
			if payload.Severity == "" {
				t.Fatalf("compaction payload = %#v, want severity", payload)
			}
			if payload.SessionState == "" {
				t.Fatalf("compaction payload = %#v, want session state", payload)
			}
			if payload.RestartGuidance == "" {
				t.Fatalf("compaction payload = %#v, want restart guidance", payload)
			}
			if payload.CacheReadTokens != 30 || payload.InputTokens != 50 || payload.CacheCreateTokens != 20 {
				t.Fatalf("compaction usage = cache_read=%d input=%d cache_create=%d, want 30, 50, 20", payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens)
			}
			if payload.PromptTokens <= 0 || payload.RawPromptTokens <= 0 {
				t.Fatalf("compaction occupancy = calibrated=%d raw=%d, want both positive", payload.PromptTokens, payload.RawPromptTokens)
			}
			if payload.PromptTokens != payload.AfterPromptTokens || payload.RawPromptTokens != payload.AfterRawPromptTokens {
				t.Fatalf("compaction after occupancy = calibrated=%d/%d raw=%d/%d, want event snapshot to match after fit", payload.PromptTokens, payload.AfterPromptTokens, payload.RawPromptTokens, payload.AfterRawPromptTokens)
			}
		case "session_health":
			payload, ok := output.AsContextSessionHealthEvent(event.Payload)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextSessionHealthEvent", event.Payload)
			}
			if payload.Severity == "compacting" {
				continue
			}
			sessionHealthCount++
			if payload.CompactionCount == 0 {
				t.Fatalf("session health payload = %#v, want compaction count", payload)
			}
			if payload.Severity == "" {
				t.Fatalf("session health payload = %#v, want severity", payload)
			}
		case "budget":
			payload, ok := output.AsContextBudgetEvent(event.Payload)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextBudgetEvent", event.Payload)
			}
			if payload.PromptTokens > 0 || payload.TotalTokens > 0 {
				budgetCount++
			}
		}
	}
	if got, want := compactionCount, 1; got != want {
		t.Fatalf("compaction events = %d, want %d", got, want)
	}
	if got, want := sessionHealthCount, 1; got != want {
		t.Fatalf("session health events = %d, want %d", got, want)
	}
	if got, want := len(compactionCounts), 1; got != want {
		t.Fatalf("compaction counts = %v, want %d entries", compactionCounts, want)
	}
	if got, want := compactionCounts[0], 2; got != want {
		t.Fatalf("first compaction count = %d, want %d", got, want)
	}
	if got, want := budgetCount, 2; got != want {
		t.Fatalf("token budget diagnostics = %d, want %d", got, want)
	}
}

func TestRunnerLargeToolResultCrossingSoftThresholdButFittingCompletes(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:      provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "big.txt"}}},
				},
				FinishReason: "tool_calls",
			},
			{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}, FinishReason: "stop"},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{Value: map[string]any{"contents": strings.Repeat("word ", 400)}}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               2400,
			MaxCompletionTokens:       16,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          64,
			NormalSummaryMaxTokens:    64,
			EmergencySummaryMaxTokens: 32,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "read the big file"}},
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 5000},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want run to complete (reqs=%d)", err, len(providerStub.requests))
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
}

// claudeLikeCompactionProvider is a stateful provider with the compaction
// transition contract. Its summary stream is one done chunk, and it records the
// requests it receives through fakeProvider.
type claudeLikeCompactionProvider struct {
	*fakeProvider
}

func (claudeLikeCompactionProvider) StatefulTranscript() bool         { return true }
func (claudeLikeCompactionProvider) SupportsStatefulCompaction() bool { return true }

// newClaudeLikeCompactionProvider returns a claudeLikeCompactionProvider whose
// summary done chunk sets ClearBackendState to clearState.
func newClaudeLikeCompactionProvider(clearState bool) claudeLikeCompactionProvider {
	return claudeLikeCompactionProvider{fakeProvider: &fakeProvider{
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			ch := make(chan provider.ChatChunk, 1)
			ch <- provider.ChatChunk{
				Delta:             provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"},
				ContentSnapshot:   true,
				Done:              true,
				FinishReason:      "stop",
				ClearBackendState: clearState,
			}
			close(ch)
			return ch, nil
		},
	}}
}

// compactionTestTurns returns n user and assistant turns.
func compactionTestTurns(n int) []Message {
	msgs := make([]Message, 0, 2*n)
	for i := range n {
		msgs = append(msgs,
			Message{Role: MessageRoleUser, Content: fmt.Sprintf("turn %d user", i)},
			Message{Role: MessageRoleAssistant, Content: fmt.Sprintf("turn %d answer", i)},
		)
	}
	return msgs
}

func compactionTestBudget() RunRequest {
	return RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SummaryMaxTokens:    128,
		},
	}
}

// TestRunnerCompactStatefulSummaryUsesTransitionContract proves a stateful
// provider with the transition contract is compacted, and that the summary
// request carries the marker, the source count and the generation's backend
// state.
func TestRunnerCompactStatefulSummaryUsesTransitionContract(t *testing.T) {
	prov := newClaudeLikeCompactionProvider(false)
	conv := compactionTestTurns(5)
	state := []byte(`{"provider":"claude_subscription","version":1}`)
	lineage := ConversationLineage{
		Generations:      []ConversationGeneration{{ID: 1, Messages: conv, BackendState: state}},
		NextGenerationID: 2,
	}
	req := compactionTestBudget()
	req.Provider = prov

	got, cleared, err := (&Runner{}).CompactWithLineage(context.Background(), req, lineage, conv)
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if cleared {
		t.Error("cleared = true for a summary that does not clear backend state")
	}
	if len(got) >= len(conv) {
		t.Errorf("conversation length = %d, want fewer than %d after compaction", len(got), len(conv))
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(prov.requests))
	}
	sent := prov.requests[0]
	if !sent.CompactionSummary {
		t.Error("summary request lacks CompactionSummary")
	}
	if sent.CompactionSourceMessages <= 0 || sent.CompactionSourceMessages >= len(conv) {
		t.Errorf("CompactionSourceMessages = %d, want between 1 and %d", sent.CompactionSourceMessages, len(conv)-1)
	}
	if string(sent.BackendState) != string(state) {
		t.Errorf("summary BackendState = %s, want the generation state %s", sent.BackendState, state)
	}
}

// TestSummarizeCompactorClearsOldGenerationBackendStateWhenSummaryClears proves
// a summary whose response clears backend state drops the old generation's
// stored state, and that the summary does not change the state of the
// generation it compacts into.
func TestSummarizeCompactorClearsOldGenerationBackendStateWhenSummaryClears(t *testing.T) {
	prov := newClaudeLikeCompactionProvider(true)
	conv := compactionTestTurns(5)
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	oldID := state.Lineage.latestGenerationID()
	state.Lineage = state.Lineage.withBackendState(oldID, []byte(`{"version":1}`), false)
	if state.Lineage.latestBackendState() == nil {
		t.Fatal("test setup: latest generation has no backend state")
	}
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	req := compactionTestBudget()
	req.Provider = prov

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 3, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("summary not applied")
	}
	found := false
	for _, gen := range outcome.State.Lineage.Generations {
		if gen.ID != oldID {
			continue
		}
		found = true
		if gen.BackendState != nil {
			t.Errorf("old generation kept backend state %s, want it cleared", gen.BackendState)
		}
	}
	if !found {
		t.Fatalf("old generation %d missing from compacted lineage", oldID)
	}
}

// TestRunnerCompactStatefulSummaryReportsClearedState proves a summary that
// clears the generation's backend state is reported, so the caller can drop that
// state from the lineage it keeps.
func TestRunnerCompactStatefulSummaryReportsClearedState(t *testing.T) {
	prov := newClaudeLikeCompactionProvider(true)
	conv := compactionTestTurns(5)
	lineage := ConversationLineage{
		Generations:      []ConversationGeneration{{ID: 1, Messages: conv, BackendState: []byte(`{"version":1}`)}},
		NextGenerationID: 2,
	}
	req := compactionTestBudget()
	req.Provider = prov

	got, cleared, err := (&Runner{}).CompactWithLineage(context.Background(), req, lineage, conv)
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if len(got) >= len(conv) {
		t.Fatalf("conversation length = %d, want fewer than %d after compaction", len(got), len(conv))
	}
	if !cleared {
		t.Error("cleared = false, want true for a summary that clears backend state")
	}
}

// TestTurnProgressorCompactionDue proves the automatic trigger: a native
// provider keeps the estimated fit, a stateful compaction provider triggers on
// CLI usage against the context window, and the hard limit always triggers.
func TestTurnProgressorCompactionDue(t *testing.T) {
	stateful := newClaudeLikeCompactionProvider(false)
	native := &fakeProvider{}
	tests := []struct {
		name  string
		prov  provider.Provider
		fit   prompt.RequestTokenBudget
		usage *provider.UsageStats
		want  bool
	}{
		{name: "native estimate triggers", prov: native, fit: prompt.RequestTokenBudget{Fits: true, ShouldCompact: true, ContextSize: 1000}, want: true},
		{name: "native ignores response usage", prov: native, fit: prompt.RequestTokenBudget{Fits: true, ContextSize: 1000}, usage: &provider.UsageStats{PromptTokens: 900}},
		{name: "native hard limit", prov: native, fit: prompt.RequestTokenBudget{Fits: false, ContextSize: 1000}, want: true},
		// PromptTokens is the total input, cache included, as the provider decodes it.
		// Adding the cache counts again would turn the 650 case into 1300.
		{name: "stateful total with cache reaches threshold", prov: stateful, fit: prompt.RequestTokenBudget{Fits: true, ContextSize: 1000}, usage: &provider.UsageStats{PromptTokens: 710, CacheReadInputTokens: 500, CacheCreationInputTokens: 200}, want: true},
		{name: "stateful total below threshold despite large cache", prov: stateful, fit: prompt.RequestTokenBudget{Fits: true, ContextSize: 1000}, usage: &provider.UsageStats{PromptTokens: 650, CacheReadInputTokens: 500, CacheCreationInputTokens: 150}},
		{name: "stateful estimate alone does not trigger", prov: stateful, fit: prompt.RequestTokenBudget{Fits: true, ShouldCompact: true, ContextSize: 1000}, usage: &provider.UsageStats{PromptTokens: 100}},
		{name: "stateful without usage does not trigger", prov: stateful, fit: prompt.RequestTokenBudget{Fits: true, ContextSize: 1000}},
		{name: "stateful hard limit triggers", prov: stateful, fit: prompt.RequestTokenBudget{Fits: false, ContextSize: 1000}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &turnProgressor{request: RunRequest{Provider: tt.prov}, lastUsage: tt.usage}
			if got := p.compactionDue(tt.fit); got != tt.want {
				t.Errorf("compactionDue() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHandleCompactionStatefulTransitionCompactsWhileFitting proves a stateful
// compaction provider that fits the hard limit still compacts when the trigger
// fires, and that a completed compaction resets the usage trigger.
func TestHandleCompactionStatefulTransitionCompactsWhileFitting(t *testing.T) {
	calls := 0
	p := newTurnProgressor(RunRequest{Provider: newClaudeLikeCompactionProvider(false)}, prompt.AssemblyOptions{}, func(context.Context, RunRequest, *RunState, int, *prompt.RequestTokenBudget, map[string]bool, *int) (bool, error) {
		calls++
		return true, nil
	})
	p.lastUsage = &provider.UsageStats{PromptTokens: 800}
	outcome := p.handleCompaction(context.Background(), RunState{}, prompt.RequestTokenBudget{Fits: true, ContextSize: 1000})
	if outcome.Error != nil || !outcome.Retry {
		t.Fatalf("handleCompaction() = %+v, want retry without error", outcome)
	}
	if calls != 1 {
		t.Errorf("compaction ran %d times, want 1", calls)
	}
	if p.lastUsage != nil {
		t.Error("lastUsage kept after a completed compaction")
	}
}
