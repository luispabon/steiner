package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// parentUsageFixture returns parent usage for model-a with a 10000-token window.
func parentUsageFixture(promptTokens int) *ParentUsage {
	return &ParentUsage{
		ModelBackendID: "model-a",
		ContextSize:    10000,
		Usage:          &provider.UsageStats{PromptTokens: promptTokens},
	}
}

func TestNewParentUsageRecordsOnlyParentResponses(t *testing.T) {
	usage := &provider.UsageStats{PromptTokens: 8000}
	tests := []struct {
		name   string
		source usagestats.Source
		usage  *provider.UsageStats
		want   bool
	}{
		{name: "parent response", source: usagestats.SourceParent, usage: usage, want: true},
		{name: "sub-agent response", source: usagestats.SourceSubAgent, usage: usage},
		{name: "advisor response", source: usagestats.SourceAdvisor, usage: usage},
		{name: "parent response without usage", source: usagestats.SourceParent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := RunRequest{
				ResolvedModel: provider.ResolvedModel{BackendModelID: "model-a"},
				ModelBudget:   prompt.ModelTokenBudget{ContextSize: 10000},
				UsageSource:   tt.source,
			}
			got := newParentUsage(req, tt.usage)
			if (got != nil) != tt.want {
				t.Fatalf("newParentUsage() = %+v, want recorded = %v", got, tt.want)
			}
			if tt.want && !reflect.DeepEqual(got, parentUsageFixture(8000)) {
				t.Errorf("newParentUsage() = %+v, want %+v", got, parentUsageFixture(8000))
			}
		})
	}
}

func TestSeedParentUsageRequiresMatchingModelAndContext(t *testing.T) {
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "model-a"},
		ModelBudget:   prompt.ModelTokenBudget{ContextSize: 10000},
	}
	tests := []struct {
		name string
		seed *ParentUsage
		want bool
	}{
		{name: "matching model and context", seed: parentUsageFixture(8000), want: true},
		{name: "model mismatch", seed: &ParentUsage{ModelBackendID: "model-b", ContextSize: 10000, Usage: &provider.UsageStats{PromptTokens: 8000}}},
		{name: "context mismatch", seed: &ParentUsage{ModelBackendID: "model-a", ContextSize: 20000, Usage: &provider.UsageStats{PromptTokens: 8000}}},
		{name: "nil seed"},
		{name: "seed without usage", seed: &ParentUsage{ModelBackendID: "model-a", ContextSize: 10000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := seedParentUsage(tt.seed, req)
			if (got != nil) != tt.want {
				t.Fatalf("seedParentUsage() = %+v, want seeded = %v", got, tt.want)
			}
			if tt.want && got == tt.seed {
				t.Error("seedParentUsage() returned the caller's seed, want a copy")
			}
		})
	}
}

func TestParentUsageCloneIsDeep(t *testing.T) {
	original := parentUsageFixture(8000)
	clone := original.Clone()
	clone.Usage.PromptTokens = 1
	clone.ContextSize = 1
	if original.Usage.PromptTokens != 8000 || original.ContextSize != 10000 {
		t.Fatalf("mutating the clone changed the original: %+v", original)
	}
	var nilUsage *ParentUsage
	if got := nilUsage.Clone(); got != nil {
		t.Errorf("Clone() of nil = %+v, want nil", got)
	}
}

// statefulRunProvider is a stateful compaction provider. Its normal responses
// report promptTokens, and its summary stream completes with one done chunk.
func statefulRunProvider(promptTokens int) claudeLikeCompactionProvider {
	prov := newClaudeLikeCompactionProvider(false)
	prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "answer"},
			Usage:   &provider.UsageStats{PromptTokens: promptTokens},
		}, nil
	}
	return prov
}

// summaryRequests counts the compaction summary requests sent to prov.
func summaryRequests(prov claudeLikeCompactionProvider) int {
	n := 0
	for _, req := range prov.requests {
		if req.CompactionSummary {
			n++
		}
	}
	return n
}

// statefulRunRequest is a parent run over a five-turn conversation on model-a
// with a 10000-token window. The window keeps the estimated fit well below the
// limit, so only the carried usage can trigger compaction.
func statefulRunRequest(prov provider.Provider, seed *ParentUsage) RunRequest {
	return RunRequest{
		Provider:           prov,
		Executor:           &fakeExecutor{},
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "model-a"},
		ModelBudget:        prompt.ModelTokenBudget{ContextSize: 10000, MaxCompletionTokens: 16, SummaryMaxTokens: 64, NormalSummaryMaxTokens: 64, EmergencySummaryMaxTokens: 32},
		Limits:             Limits{MaxTurns: 8},
		SourceConversation: compactionTestTurns(),
		SeedParentUsage:    seed,
	}
}

// TestRunRecordsParentUsageOnlyForParentRuns proves a parent response records
// its usage, and a sub-agent run never does.
func TestRunRecordsParentUsageOnlyForParentRuns(t *testing.T) {
	tests := []struct {
		name   string
		source usagestats.Source
		want   *ParentUsage
	}{
		{name: "parent run", source: usagestats.SourceParent, want: parentUsageFixture(8000)},
		{name: "sub-agent run", source: usagestats.SourceSubAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := statefulRunProvider(8000)
			req := statefulRunRequest(prov, nil)
			req.UsageSource = tt.source
			state, err := NewRunner().Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !reflect.DeepEqual(state.ParentUsage, tt.want) {
				t.Errorf("state.ParentUsage = %+v, want %+v", state.ParentUsage, tt.want)
			}
		})
	}
}

// TestRunKeepsParentUsageWhenLaterCallFails proves a successful response's usage
// survives an error later in the same run.
func TestRunKeepsParentUsageWhenLaterCallFails(t *testing.T) {
	// Usage stays below the compaction threshold so the second call is reached
	// without compacting, which would clear the usage.
	calls := 0
	prov := statefulRunProvider(100)
	prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		calls++
		if calls == 1 {
			return provider.ChatResponse{
				Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read"}}},
				Usage:   &provider.UsageStats{PromptTokens: 100},
			}, nil
		}
		return provider.ChatResponse{}, errors.New("boom")
	}
	req := statefulRunRequest(prov, nil)
	req.Executor = &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
		return "done", nil
	}}

	state, err := NewRunner().Run(context.Background(), req)
	if err == nil {
		t.Fatal("Run() error = nil, want the second call's error")
	}
	if !reflect.DeepEqual(state.ParentUsage, parentUsageFixture(100)) {
		t.Errorf("state.ParentUsage after error = %+v, want %+v", state.ParentUsage, parentUsageFixture(100))
	}
}

// TestRunSeedsParentUsageForStatefulCompaction proves a seeded usage triggers
// stateful compaction at the start of a run only when it matches the run's model
// and context window.
func TestRunSeedsParentUsageForStatefulCompaction(t *testing.T) {
	tests := []struct {
		name        string
		seed        *ParentUsage
		wantSummary int
	}{
		{name: "matching seed compacts", seed: parentUsageFixture(8000), wantSummary: 1},
		{name: "model mismatch is ignored", seed: &ParentUsage{ModelBackendID: "model-b", ContextSize: 10000, Usage: &provider.UsageStats{PromptTokens: 8000}}},
		{name: "context mismatch is ignored", seed: &ParentUsage{ModelBackendID: "model-a", ContextSize: 20000, Usage: &provider.UsageStats{PromptTokens: 8000}}},
		{name: "nil seed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := statefulRunProvider(100)
			if _, err := NewRunner().Run(context.Background(), statefulRunRequest(prov, tt.seed)); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := summaryRequests(prov); got != tt.wantSummary {
				t.Errorf("summary requests = %d, want %d", got, tt.wantSummary)
			}
		})
	}
}

// TestRunCarriedParentUsageCompactsLaterAssistantOnlyRun proves the cross-run
// path: the first run's recorded usage, carried as the next run's seed, triggers
// compaction in a run whose turn is assistant-only.
func TestRunCarriedParentUsageCompactsLaterAssistantOnlyRun(t *testing.T) {
	firstProv := statefulRunProvider(8000)
	first, err := NewRunner().Run(context.Background(), statefulRunRequest(firstProv, nil))
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if summaryRequests(firstProv) != 0 {
		t.Fatal("first run compacted without a seed")
	}

	secondProv := statefulRunProvider(100)
	req := statefulRunRequest(secondProv, first.ParentUsage)
	req.SourceConversation = first.Conversation
	if _, err := NewRunner().Run(context.Background(), req); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got := summaryRequests(secondProv); got != 1 {
		t.Errorf("summary requests in second run = %d, want 1", got)
	}
}

// TestHandleCompactionClearsParentUsageOnlyWhenCompacted proves a completed
// compaction drops the recorded parent usage, and a compaction that did not
// apply keeps it.
func TestHandleCompactionClearsParentUsageOnlyWhenCompacted(t *testing.T) {
	tests := []struct {
		name      string
		compacted bool
		wantUsage *ParentUsage
	}{
		{name: "applied compaction clears usage", compacted: true},
		{name: "no candidate keeps usage", compacted: false, wantUsage: parentUsageFixture(8000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTurnProgressor(RunRequest{Provider: newClaudeLikeCompactionProvider(false)}, prompt.AssemblyOptions{}, func(context.Context, RunRequest, *RunState, int, *prompt.RequestTokenBudget, map[string]bool, *int) (bool, bool, error) {
				return tt.compacted, tt.compacted, nil
			})
			p.lastUsage = parentUsageFixture(8000).Usage
			state := RunState{ParentUsage: parentUsageFixture(8000)}
			outcome := p.handleCompaction(context.Background(), state, prompt.RequestTokenBudget{Fits: true, ContextSize: 10000})
			if !reflect.DeepEqual(outcome.State.ParentUsage, tt.wantUsage) {
				t.Errorf("state.ParentUsage = %+v, want %+v", outcome.State.ParentUsage, tt.wantUsage)
			}
			if tt.compacted && p.lastUsage != nil {
				t.Error("lastUsage kept after a completed compaction")
			}
		})
	}
}

// TestHandleCompactionNotAppliedDoesNotArmStatefulGuard proves a candidate that
// is attempted but does not apply leaves the stateful guard unarmed and keeps
// the usage. The retry moves past the candidate and ends, so the following
// high-usage response does not stop the run.
func TestHandleCompactionNotAppliedDoesNotArmStatefulGuard(t *testing.T) {
	prov := newClaudeLikeCompactionProvider(false)
	// Two messages are all retained turns, so the stage has no source and does not apply.
	conv := compactionTestTurns()[:2]
	req := statefulRunRequest(prov, nil)
	req.SourceConversation = conv
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, (&Runner{}).compactConversationForBudget)
	p.lastUsage = &provider.UsageStats{PromptTokens: 9500}
	fit := prompt.RequestTokenBudget{Fits: true, ContextSize: 10000}

	const maxAttempts = 3
	retries := 0
	for range maxAttempts {
		outcome := p.handleCompaction(context.Background(), state, fit)
		if outcome.Error != nil || outcome.Stop {
			t.Fatalf("handleCompaction() = %+v, want no error or stop while the request fits", outcome)
		}
		if !outcome.Retry {
			break
		}
		retries++
	}
	if retries != 1 {
		t.Fatalf("retries = %d, want 1 retry for the attempted candidate", retries)
	}
	if p.awaitingPostCompaction {
		t.Error("awaitingPostCompaction armed by a compaction that did not apply")
	}
	if p.lastUsage == nil {
		t.Error("lastUsage cleared by a compaction that did not apply")
	}

	p.observeStatefulUsage(&provider.UsageStats{PromptTokens: 9500})
	if outcome := p.handleCompaction(context.Background(), state, fit); outcome.Error != nil || outcome.Stop {
		t.Errorf("handleCompaction() after a high-usage response = %+v, want no cap stop", outcome)
	}
}
