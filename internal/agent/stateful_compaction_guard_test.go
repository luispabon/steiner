package agent

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestStatefulUsageReachesSelectedThresholdUsesExplicitAbsoluteTarget(t *testing.T) {
	budget := prompt.ModelTokenBudget{ContextSize: 10000, CompactionThresholdTokens: 4200}
	if !statefulUsageReachesSelectedThreshold(&provider.UsageStats{PromptTokens: 4200}, 10000, budget) {
		t.Fatal("statefulUsageReachesSelectedThreshold() = false at explicit threshold")
	}
	if statefulUsageReachesSelectedThreshold(&provider.UsageStats{PromptTokens: 4199}, 10000, budget) {
		t.Fatal("statefulUsageReachesSelectedThreshold() = true below explicit threshold")
	}
}

func TestStatefulUsageAboveThresholdPercentFallback(t *testing.T) {
	tests := []struct {
		name         string
		percent      float64
		promptTokens int
		want         bool
	}{
		{name: "zero percent uses default at threshold", percent: 0, promptTokens: 7000, want: true},
		{name: "zero percent uses default below threshold", percent: 0, promptTokens: 6999},
		{name: "negative percent uses default", percent: -1, promptTokens: 7000, want: true},
		{name: "NaN percent uses default at threshold", percent: math.NaN(), promptTokens: 7000, want: true},
		{name: "NaN percent uses default below threshold", percent: math.NaN(), promptTokens: 6999},
		{name: "positive percent overrides default at threshold", percent: 0.5, promptTokens: 5000, want: true},
		{name: "positive percent overrides default below threshold", percent: 0.5, promptTokens: 4999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &provider.UsageStats{PromptTokens: tt.promptTokens}
			if got := statefulUsageAboveThreshold(usage, 10000, tt.percent); got != tt.want {
				t.Errorf("statefulUsageAboveThreshold(%d, 10000, %v) = %v, want %v", tt.promptTokens, tt.percent, got, tt.want)
			}
		})
	}
}

func TestStatefulReleaseTokens(t *testing.T) {
	tests := []struct {
		name        string
		floor       int
		contextSize int
		want        int
	}{
		{name: "floor plus ten percent", floor: 7500, contextSize: 10000, want: 8500},
		{name: "hysteresis rounds up", floor: 500, contextSize: 1001, want: 601},
		{name: "capped at ninety percent", floor: 8500, contextSize: 10000, want: 9000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statefulReleaseTokens(tt.floor, tt.contextSize); got != tt.want {
				t.Errorf("statefulReleaseTokens(%d, %d) = %d, want %d", tt.floor, tt.contextSize, got, tt.want)
			}
		})
	}
}

func TestStatefulCompactionDue(t *testing.T) {
	tests := []struct {
		name              string
		floor             int
		promptTokens      int
		hardLimitExceeded bool
		capReached        bool
		want              bool
	}{
		{name: "no floor below threshold", promptTokens: 6999},
		{name: "no floor at threshold", promptTokens: 7000, want: true},
		{name: "floor holds one below release", floor: 7500, promptTokens: 8499},
		{name: "floor releases at release point", floor: 7500, promptTokens: 8500, want: true},
		{name: "floor near cap holds below cap", floor: 8500, promptTokens: 8999},
		{name: "floor near cap releases at cap", floor: 8500, promptTokens: 9000, want: true},
		{name: "hard limit overrides floor", floor: 7500, promptTokens: 100, hardLimitExceeded: true, want: true},
		{name: "cap stops regardless of usage", promptTokens: 100, capReached: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &turnProgressor{
				request:            RunRequest{Provider: newClaudeLikeCompactionProvider(false)},
				lastUsage:          &provider.UsageStats{PromptTokens: tt.promptTokens},
				statefulFloor:      tt.floor,
				statefulCapReached: tt.capReached,
			}
			fit := prompt.RequestTokenBudget{Fits: !tt.hardLimitExceeded, ContextSize: 10000}
			if got := p.compactionDue(fit); got != tt.want {
				t.Errorf("compactionDue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestObserveStatefulUsage(t *testing.T) {
	usage := func(promptTokens int) *provider.UsageStats {
		return &provider.UsageStats{PromptTokens: promptTokens}
	}
	tests := []struct {
		name        string
		nonStateful bool
		awaiting    bool
		floor       int
		usage       *provider.UsageStats
		wantFloor   int
		wantCap     bool
	}{
		{name: "post compaction below threshold arms nothing", awaiting: true, usage: usage(6999)},
		{name: "post compaction at threshold arms floor", awaiting: true, usage: usage(7000), wantFloor: 7000},
		{name: "post compaction below cap arms floor", awaiting: true, usage: usage(8999), wantFloor: 8999},
		{name: "post compaction at cap sets cap", awaiting: true, usage: usage(9000), wantCap: true},
		{name: "post compaction without usage arms nothing", awaiting: true},
		{name: "armed floor holds below release", floor: 7500, usage: usage(8499), wantFloor: 7500},
		{name: "armed floor releases at release point", floor: 7500, usage: usage(8500)},
		{name: "non-stateful provider changes nothing", nonStateful: true, floor: 7500, usage: usage(9500), wantFloor: 7500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var prov provider.Provider = newClaudeLikeCompactionProvider(false)
			if tt.nonStateful {
				prov = &fakeProvider{}
			}
			p := &turnProgressor{
				request:                RunRequest{Provider: prov, ModelBudget: prompt.ModelTokenBudget{ContextSize: 10000}},
				awaitingPostCompaction: tt.awaiting,
				statefulFloor:          tt.floor,
			}
			p.observeStatefulUsage(tt.usage)
			if p.statefulFloor != tt.wantFloor {
				t.Errorf("statefulFloor = %d, want %d", p.statefulFloor, tt.wantFloor)
			}
			if p.statefulCapReached != tt.wantCap {
				t.Errorf("statefulCapReached = %v, want %v", p.statefulCapReached, tt.wantCap)
			}
			if p.awaitingPostCompaction {
				t.Error("awaitingPostCompaction still set after a response")
			}
		})
	}
}

// scriptedUsageStep is one parent response: its PromptTokens and whether it asks
// for a tool call, which makes the run take another turn.
type scriptedUsageStep struct {
	promptTokens int
	toolCall     bool
}

// scriptedStatefulProvider is a stateful compaction provider whose parent
// responses follow steps in order. The last step repeats once steps run out.
func scriptedStatefulProvider(steps ...scriptedUsageStep) claudeLikeCompactionProvider {
	prov := newClaudeLikeCompactionProvider(false)
	calls := 0
	prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		step := steps[min(calls, len(steps)-1)]
		calls++
		resp := provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "answer"},
			Usage:   &provider.UsageStats{PromptTokens: step.promptTokens},
		}
		if step.toolCall {
			resp.Message.ToolCalls = []provider.ToolCall{{ID: "call-1", Name: "read"}}
		}
		return resp, nil
	}
	return prov
}

// TestRunStatefulFloorHoldsThenReleases proves that after a seeded compaction, a
// response that arms a floor suppresses the soft trigger until usage reaches the
// release point, then compacts again.
func TestRunStatefulFloorHoldsThenReleases(t *testing.T) {
	prov := scriptedStatefulProvider(
		scriptedUsageStep{promptTokens: 7500, toolCall: true},
		scriptedUsageStep{promptTokens: 8000, toolCall: true},
		scriptedUsageStep{promptTokens: 8600, toolCall: true},
		scriptedUsageStep{promptTokens: 100},
	)
	req := scriptedRunRequest(prov, parentUsageFixture(8000))
	if _, err := NewRunner().Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// One compaction from the seed, one at release (8600 >= 8500). The 7500 and
	// 8000 responses are held.
	if got := summaryRequests(prov); got != 2 {
		t.Errorf("summary requests = %d, want 2", got)
	}
}

// TestRunStatefulCapStopsWithoutCompacting proves a response at the cap ends
// the run at its next compaction decision, without a further compaction, and
// emits the critical session health event.
func TestRunStatefulCapStopsWithoutCompacting(t *testing.T) {
	prov := scriptedStatefulProvider(scriptedUsageStep{promptTokens: 9500, toolCall: true})
	rec := &eventRecorder{}
	req := scriptedRunRequest(prov, parentUsageFixture(8000))
	req.Events = rec.sink()
	_, err := NewRunner().Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "request exceeds context window") {
		t.Fatalf("Run() error = %v, want request exceeds context window", err)
	}
	if got := summaryRequests(prov); got != 1 {
		t.Errorf("summary requests = %d, want 1 (the seeded compaction only)", got)
	}
	if !hasCriticalSessionHealth(rec.snapshot()) {
		t.Error("no critical likely_lossy session health event with critical restart guidance")
	}
}

// hasCriticalSessionHealth reports whether events include a critical,
// likely_lossy session health event carrying the critical restart guidance.
func hasCriticalSessionHealth(events []output.Event) bool {
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics || output.ContextDiagnosticKind(event.Payload) != "session_health" {
			continue
		}
		payload, ok := output.AsContextSessionHealthEvent(event.Payload)
		if ok && payload.Severity == "critical" && payload.SessionState == "likely_lossy" && payload.RestartGuidance == compactionRestartGuidanceCritical {
			return true
		}
	}
	return false
}

// TestRunCarriedStatefulFloor proves a carried floor holds a seeded run below
// its release point, and that an unarmed seed compacts at the threshold.
func TestRunCarriedStatefulFloor(t *testing.T) {
	tests := []struct {
		name        string
		floor       int
		wantSummary int
	}{
		{name: "armed floor holds", floor: 7500, wantSummary: 0},
		{name: "unarmed seed compacts at threshold", wantSummary: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed := parentUsageFixture(7500)
			seed.StatefulFloor = tt.floor
			prov := statefulRunProvider(8000)
			if _, err := NewRunner().Run(context.Background(), statefulRunRequest(prov, seed)); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := summaryRequests(prov); got != tt.wantSummary {
				t.Errorf("summary requests = %d, want %d", got, tt.wantSummary)
			}
		})
	}
}

// TestSeedParentUsageKeepsStatefulFloorOnExactMatch proves the floor survives a
// seed on the same model and context window, and a mismatched seed is dropped.
func TestSeedParentUsageKeepsStatefulFloorOnExactMatch(t *testing.T) {
	seed := parentUsageFixture(8000)
	seed.StatefulFloor = 7500
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "model-a"},
		ModelBudget:   prompt.ModelTokenBudget{ContextSize: 10000},
	}
	if got := seedParentUsage(seed, req); got == nil || got.StatefulFloor != 7500 {
		t.Errorf("seedParentUsage() = %+v, want floor 7500 kept", got)
	}
	req.ModelBudget.ContextSize = 20000
	if got := seedParentUsage(seed, req); got != nil {
		t.Errorf("seedParentUsage() on context mismatch = %+v, want nil", got)
	}
}

// scriptedRunRequest is statefulRunRequest with an executor that answers tool
// calls, so scripted responses that ask for a tool take another turn.
func scriptedRunRequest(prov provider.Provider, seed *ParentUsage) RunRequest {
	req := statefulRunRequest(prov, seed)
	req.Executor = &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
		return "done", nil
	}}
	return req
}
