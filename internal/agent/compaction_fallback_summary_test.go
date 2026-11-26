package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// compactionFallbackSummaryFixture returns a main request on prov that records
// usage to recorder and emits to a no-op sink. The main budget is larger than
// the budget from compactionFallbackSummaryBudget.
func compactionFallbackSummaryFixture(prov provider.Provider, recorder *testRecorder) RunRequest {
	req := compactionFallbackTestRequest(prov)
	req.ModelBudget = prompt.ModelTokenBudget{ContextSize: 8000, NormalSummaryMaxTokens: 512}
	req.Prompt.ModelBudget = req.ModelBudget
	// Prompt assembly resolves skill names against roots; these tests have none.
	req.Prompt.SkillsRoots = nil
	req.Prompt.SkillNames = nil
	req.UsageRecorder = recorder
	req.Events = output.SinkFunc(func(output.Event) {})
	return req
}

// compactionFallbackSummaryTarget returns a fallback target that sends its
// summary to prov with the given budget.
func compactionFallbackSummaryTarget(prov provider.Provider, budget prompt.ModelTokenBudget) CompactionTarget {
	target := compactionFallbackTestTarget()
	target.Provider = prov
	target.ModelBudget = budget
	return target
}

// compactionFallbackSummaryBudget is the fallback target budget used by these
// tests unless a test overrides it.
func compactionFallbackSummaryBudget() prompt.ModelTokenBudget {
	return prompt.ModelTokenBudget{ContextSize: 4000, NormalSummaryMaxTokens: 128}
}

func compactionFallbackSummaryMessages() []Message {
	return []Message{
		{Role: MessageRoleUser, Content: "first question"},
		{Role: MessageRoleAssistant, Content: "first answer"},
		{Role: MessageRoleUser, Content: "second question"},
		{Role: MessageRoleAssistant, Content: "second answer"},
	}
}

// compactionFallbackSummaryResponse returns a summary response. clearBackend
// sets ClearBackendState, as an in-place summary does on a stateful provider.
func compactionFallbackSummaryResponse(clearBackend bool) provider.ChatResponse {
	return provider.ChatResponse{
		Message:           provider.Message{Role: provider.MessageRoleAssistant, Content: "summary text"},
		Usage:             &provider.UsageStats{PromptTokens: 50, CompletionTokens: 10},
		FinishReason:      "stop",
		ClearBackendState: clearBackend,
	}
}

// compactionFallbackSummaryParams returns normal-mode stage params that
// summarize the first two messages and retain the last two.
func compactionFallbackSummaryParams(maxTokens int, summaryReq *RunRequest) summarizeCompactionStageParams {
	msgs := compactionFallbackSummaryMessages()
	return summarizeCompactionStageParams{
		sourceMessages:   msgs[:2],
		retainedMessages: msgs[2:],
		mode:             prompt.CompactionModeNormal,
		maxTokens:        maxTokens,
		summaryReq:       summaryReq,
	}
}

func TestSummarizeCompactionStageFallbackRunsOnTarget(t *testing.T) {
	t.Parallel()
	mainProv := &fakeProvider{}
	var rec eventRecorder
	var eventsAtSummary int
	targetProv := &fakeProvider{}
	targetProv.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		// The selection must already be announced when the summary call runs.
		eventsAtSummary = len(rec.snapshot())
		return compactionFallbackSummaryResponse(false), nil
	}
	recorder := &testRecorder{}
	req := compactionFallbackSummaryFixture(mainProv, recorder)
	target := compactionFallbackSummaryTarget(targetProv, compactionFallbackSummaryBudget())
	fallback, err := compactionFallbackRequest(req, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	msgs := compactionFallbackSummaryMessages()
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	candidate := ConversationCandidate{Messages: msgs}
	maxTokens := compactionSummaryMaxTokensForMode(target.ModelBudget, prompt.CompactionModeNormal)

	params := compactionFallbackSummaryParams(maxTokens, &fallback)
	params.announce = &compactionFallbackAnnouncement{sink: rec.sink(), turn: 1, model: compactionModelName(target.ResolvedModel), reason: "no recent request"}
	outcome, err := summarizeCompactionStage(context.Background(), req, state, 1, candidate, params)
	if err != nil {
		t.Fatalf("summarizeCompactionStage() error = %v", err)
	}
	if !outcome.Applied {
		t.Error("Applied = false, want true")
	}
	if eventsAtSummary != 1 {
		t.Errorf("selection events before the summary call = %d, want 1", eventsAtSummary)
	}
	selected := compactionEventPayloads(t, rec.snapshot())
	if len(selected) != 1 || selected[0].Severity != "compacting" || selected[0].CompactionModel != "anthropic/claude-small" {
		t.Errorf("selection events = %+v, want one compacting event for anthropic/claude-small", selected)
	}
	if got := len(mainProv.requests); got != 0 {
		t.Errorf("main provider requests = %d, want 0", got)
	}
	if got := len(targetProv.requests); got != 1 {
		t.Fatalf("target provider requests = %d, want 1", got)
	}
	chatReq := targetProv.requests[0]
	if chatReq.Model != "claude-small" {
		t.Errorf("Model = %q, want claude-small", chatReq.Model)
	}
	if chatReq.Params["temperature"] != 0.2 {
		t.Errorf("Params[temperature] = %v, want 0.2 from the target", chatReq.Params["temperature"])
	}
	if chatReq.MaxTokens == nil || *chatReq.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %v, want %d from the target budget", chatReq.MaxTokens, maxTokens)
	}
	if len(recorder.obs) != 1 {
		t.Fatalf("recorded observations = %d, want 1", len(recorder.obs))
	}
	obs := recorder.obs[0]
	if obs.ProviderAlias != "anthropic" || obs.BackendModelID != "claude-small" {
		t.Errorf("usage attributed to %s/%s, want anthropic/claude-small", obs.ProviderAlias, obs.BackendModelID)
	}
	if obs.Source != usagestats.SourceSubAgent {
		t.Errorf("usage Source = %v, want SourceSubAgent", obs.Source)
	}
}

func TestSummarizeCompactionStageFallbackNotFitReturnsSentinel(t *testing.T) {
	t.Parallel()
	mainProv := &fakeProvider{}
	var rec eventRecorder
	targetProv := &fakeProvider{}
	req := compactionFallbackSummaryFixture(mainProv, &testRecorder{})
	target := compactionFallbackSummaryTarget(targetProv, prompt.ModelTokenBudget{ContextSize: 1, NormalSummaryMaxTokens: 128})
	fallback, err := compactionFallbackRequest(req, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	msgs := compactionFallbackSummaryMessages()
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	candidate := ConversationCandidate{Messages: msgs}

	params := compactionFallbackSummaryParams(128, &fallback)
	params.announce = &compactionFallbackAnnouncement{sink: rec.sink(), turn: 1, model: compactionModelName(target.ResolvedModel), reason: "no recent request"}
	outcome, err := summarizeCompactionStage(context.Background(), req, state, 1, candidate, params)
	if !errors.Is(err, errCompactionFallbackNotFit) {
		t.Fatalf("error = %v, want errCompactionFallbackNotFit", err)
	}
	if outcome.Applied {
		t.Error("Applied = true, want false")
	}
	if got := len(targetProv.requests); got != 0 {
		t.Errorf("target provider requests = %d, want 0 when the summary does not fit", got)
	}
	if got := len(compactionEventPayloads(t, rec.snapshot())); got != 0 {
		t.Errorf("selection events = %d, want 0 when the fit check fails", got)
	}
	if got := len(mainProv.requests); got != 0 {
		t.Errorf("main provider requests = %d, want 0", got)
	}
}

func TestSummarizeCompactionStagesBudgetSplit(t *testing.T) {
	t.Parallel()
	mainReq := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	target := compactionFallbackSummaryTarget(&fakeProvider{}, compactionFallbackSummaryBudget())
	fallback, err := compactionFallbackRequest(mainReq, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	mainBudget := mainReq.ModelBudget
	targetBudget := target.ModelBudget

	tests := []struct {
		name          string
		summaryReq    *RunRequest
		wantMaxTokens int
	}{
		{name: "main summary uses main budget", summaryReq: nil, wantMaxTokens: compactionSummaryMaxTokensForMode(mainBudget, prompt.CompactionModeNormal)},
		{name: "fallback summary uses target budget", summaryReq: &fallback, wantMaxTokens: compactionSummaryMaxTokensForMode(targetBudget, prompt.CompactionModeNormal)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stageParams []summarizeCompactionStageParams
			var stageBudgets, fitBudgets []prompt.ModelTokenBudget
			stages := summarizeCompactionStages{
				stageRunner: func(_ context.Context, req RunRequest, state RunState, _ int, candidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
					stageParams = append(stageParams, params)
					stageBudgets = append(stageBudgets, req.ModelBudget)
					return CompactionOutcome{Applied: true, State: state, Candidate: candidate, Fit: prompt.RequestTokenBudget{Fits: true}}, nil
				},
				fitRunner: func(_ context.Context, req RunRequest, _ RunState) (prompt.RequestTokenBudget, error) {
					fitBudgets = append(fitBudgets, req.ModelBudget)
					return prompt.RequestTokenBudget{Fits: true}, nil
				},
				summaryReq: tt.summaryReq,
			}
			msgs := compactionFallbackSummaryMessages()
			state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
			candidate := ConversationCandidate{Messages: msgs}

			if _, err := stages.run(context.Background(), mainReq, state, 1, candidate); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if len(stageParams) != 1 {
				t.Fatalf("stage calls = %d, want 1", len(stageParams))
			}
			if got := stageParams[0].maxTokens; got != tt.wantMaxTokens {
				t.Errorf("summary maxTokens = %d, want %d", got, tt.wantMaxTokens)
			}
			if got := stageParams[0].summaryReq; got != tt.summaryReq {
				t.Errorf("stage summaryReq = %p, want %p", got, tt.summaryReq)
			}
			for _, got := range stageBudgets {
				if got != mainBudget {
					t.Errorf("stage request budget = %+v, want main budget %+v", got, mainBudget)
				}
			}
			for _, got := range fitBudgets {
				if got != mainBudget {
					t.Errorf("fit request budget = %+v, want main budget %+v", got, mainBudget)
				}
			}
		})
	}
}

func TestSummarizeCompactionStagesEmergencyUsesSummaryRequest(t *testing.T) {
	t.Parallel()
	mainReq := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	mainReq.ModelBudget.EmergencySummaryMaxTokens = 256
	target := compactionFallbackSummaryTarget(&fakeProvider{}, prompt.ModelTokenBudget{ContextSize: 4000, NormalSummaryMaxTokens: 128, EmergencySummaryMaxTokens: 32})
	fallback, err := compactionFallbackRequest(mainReq, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	msgs := compactionFallbackSummaryMessages()
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	candidate := ConversationCandidate{Messages: msgs}
	mainBudget := mainReq.ModelBudget

	tests := []struct {
		name          string
		normalApplied bool
		summaryReq    *RunRequest
		wantStages    int
		wantMaxTokens int
	}{
		{name: "normal not applied, emergency on main", normalApplied: false, summaryReq: nil, wantStages: 1, wantMaxTokens: 256},
		{name: "normal not applied, emergency on fallback", normalApplied: false, summaryReq: &fallback, wantStages: 1, wantMaxTokens: 32},
		{name: "normal applied, second stage on main", normalApplied: true, summaryReq: nil, wantStages: 2, wantMaxTokens: 256},
		{name: "normal applied, second stage on fallback", normalApplied: true, summaryReq: &fallback, wantStages: 2, wantMaxTokens: 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stageParams []summarizeCompactionStageParams
			var fitBudgets []prompt.ModelTokenBudget
			stages := summarizeCompactionStages{
				stageRunner: func(_ context.Context, _ RunRequest, st RunState, _ int, cand ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
					stageParams = append(stageParams, params)
					applied := tt.normalApplied || params.mode == prompt.CompactionModeEmergency
					return CompactionOutcome{Applied: applied, State: st, Candidate: cand, Fit: prompt.RequestTokenBudget{Fits: true}}, nil
				},
				fitRunner: func(_ context.Context, req RunRequest, _ RunState) (prompt.RequestTokenBudget, error) {
					fitBudgets = append(fitBudgets, req.ModelBudget)
					if tt.normalApplied && len(fitBudgets) == 1 {
						// The fit after the normal stage still needs emergency compaction.
						return prompt.RequestTokenBudget{ContextSize: 100, PromptUsage: 0.9}, nil
					}
					return prompt.RequestTokenBudget{ContextSize: 100, PromptUsage: 0.1, Fits: true}, nil
				},
				summaryReq: tt.summaryReq,
			}

			outcome, err := stages.run(context.Background(), mainReq, state, 1, candidate)
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if outcome.StageCount != tt.wantStages {
				t.Errorf("StageCount = %d, want %d", outcome.StageCount, tt.wantStages)
			}
			if got := len(stageParams); got != 2 {
				t.Fatalf("stage calls = %d, want 2 (normal, then emergency)", got)
			}
			for i, params := range stageParams {
				if params.summaryReq != tt.summaryReq {
					t.Errorf("stage %d summaryReq = %p, want %p", i, params.summaryReq, tt.summaryReq)
				}
			}
			emergency := stageParams[1]
			if emergency.mode != prompt.CompactionModeEmergency {
				t.Errorf("second stage mode = %v, want emergency", emergency.mode)
			}
			if emergency.maxTokens != tt.wantMaxTokens {
				t.Errorf("emergency maxTokens = %d, want %d", emergency.maxTokens, tt.wantMaxTokens)
			}
			if len(fitBudgets) == 0 {
				t.Fatal("fit runner not called")
			}
			for _, got := range fitBudgets {
				if got != mainBudget {
					t.Errorf("fit request budget = %+v, want main budget %+v", got, mainBudget)
				}
			}
		})
	}
}

func TestSummarizeCompactionStagesFallbackSentinelThroughRun(t *testing.T) {
	t.Parallel()
	mainReq := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	target := compactionFallbackSummaryTarget(&fakeProvider{}, compactionFallbackSummaryBudget())
	fallback, err := compactionFallbackRequest(mainReq, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	msgs := compactionFallbackSummaryMessages()
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	candidate := ConversationCandidate{Messages: msgs}
	archive := [][]Message{msgs[:2]}
	needsEmergency := prompt.RequestTokenBudget{ContextSize: 100, PromptUsage: 0.9}

	tests := []struct {
		name       string
		sentinelAt int
		wantCalls  int
	}{
		{name: "first stage sentinel skips emergency", sentinelAt: 1, wantCalls: 1},
		{name: "second stage sentinel discards first stage", sentinelAt: 2, wantCalls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			stages := summarizeCompactionStages{
				stageRunner: func(_ context.Context, _ RunRequest, st RunState, _ int, cand ConversationCandidate, _ summarizeCompactionStageParams) (CompactionOutcome, error) {
					calls++
					if calls == tt.sentinelAt {
						return CompactionOutcome{}, errCompactionFallbackNotFit
					}
					return CompactionOutcome{Applied: true, State: st, Candidate: cand, Fit: needsEmergency, pendingArchive: archive}, nil
				},
				fitRunner: func(_ context.Context, _ RunRequest, _ RunState) (prompt.RequestTokenBudget, error) {
					return needsEmergency, nil
				},
				summaryReq: &fallback,
			}

			outcome, err := stages.run(context.Background(), mainReq, state, 1, candidate)
			if !errors.Is(err, errCompactionFallbackNotFit) {
				t.Fatalf("run() error = %v, want errCompactionFallbackNotFit", err)
			}
			if calls != tt.wantCalls {
				t.Errorf("stage calls = %d, want %d", calls, tt.wantCalls)
			}
			if !reflect.ValueOf(outcome).IsZero() {
				t.Errorf("outcome = %+v, want zero value on sentinel error (no state or archive)", outcome)
			}
		})
	}
}

func TestBuildCompactionRequestFallbackOmitsBackendState(t *testing.T) {
	t.Parallel()
	mainReq := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	target := compactionFallbackSummaryTarget(&fakeProvider{}, compactionFallbackSummaryBudget())
	fallback, err := compactionFallbackRequest(mainReq, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	msgs := compactionFallbackSummaryMessages()
	lineage := newConversationLineage(msgs).WithNewGenerationState(nil, msgs, json.RawMessage(`{"previous_response_id":"resp_1"}`))
	state := RunState{Conversation: msgs, Lineage: lineage}
	candidate := ConversationCandidate{Messages: msgs}

	tests := []struct {
		name             string
		req              RunRequest
		wantBackendState bool
		wantModel        string
	}{
		{name: "main request carries latest backend state", req: mainReq, wantBackendState: true, wantModel: "gpt-main"},
		{name: "fallback request carries no backend state", req: fallback, wantBackendState: false, wantModel: "claude-small"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chatReq, _, _, err := buildCompactionRequestWithMode(context.Background(), tt.req, state, candidate, prompt.CompactionModeNormal, 128, "")
			if err != nil {
				t.Fatalf("buildCompactionRequestWithMode() error = %v", err)
			}
			if got := len(chatReq.BackendState) > 0; got != tt.wantBackendState {
				t.Errorf("BackendState present = %v, want %v", got, tt.wantBackendState)
			}
			if chatReq.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", chatReq.Model, tt.wantModel)
			}
		})
	}
}

func TestSummarizeCompactionStageClearBackendStateScope(t *testing.T) {
	t.Parallel()
	msgs := compactionFallbackSummaryMessages()
	lineage := newConversationLineage(msgs).WithNewGenerationState(nil, msgs, json.RawMessage(`{"previous_response_id":"resp_1"}`))
	latestID := lineage.latestGenerationID()

	tests := []struct {
		name        string
		fallback    bool
		wantCleared bool
	}{
		{name: "main summary clears the latest backend state", fallback: false, wantCleared: true},
		{name: "fallback summary keeps the main backend state", fallback: true, wantCleared: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mainProv := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(true)}}
			targetProv := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(true)}}
			req := compactionFallbackSummaryFixture(mainProv, &testRecorder{})
			target := compactionFallbackSummaryTarget(targetProv, compactionFallbackSummaryBudget())
			fallback, err := compactionFallbackRequest(req, target)
			if err != nil {
				t.Fatalf("compactionFallbackRequest() error = %v", err)
			}
			var summaryReq *RunRequest
			if tt.fallback {
				summaryReq = &fallback
			}

			state := RunState{Conversation: msgs, Lineage: lineage}
			candidate := ConversationCandidate{Messages: msgs}
			outcome, err := summarizeCompactionStage(context.Background(), req, state, 1, candidate, compactionFallbackSummaryParams(128, summaryReq))
			if err != nil {
				t.Fatalf("summarizeCompactionStage() error = %v", err)
			}

			wantMain, wantTarget := 1, 0
			if tt.fallback {
				wantMain, wantTarget = 0, 1
			}
			if got := len(mainProv.requests); got != wantMain {
				t.Errorf("main provider requests = %d, want %d", got, wantMain)
			}
			if got := len(targetProv.requests); got != wantTarget {
				t.Errorf("target provider requests = %d, want %d", got, wantTarget)
			}

			found, cleared := false, false
			for _, gen := range outcome.State.Lineage.Generations {
				if gen.ID == latestID {
					found = true
					cleared = len(gen.BackendState) == 0
				}
			}
			if !found {
				t.Fatalf("generation %d missing from outcome lineage", latestID)
			}
			if cleared != tt.wantCleared {
				t.Errorf("latest generation backend state cleared = %v, want %v", cleared, tt.wantCleared)
			}
			if outcome.BackendStateCleared != tt.wantCleared {
				t.Errorf("outcome.BackendStateCleared = %v, want %v", outcome.BackendStateCleared, tt.wantCleared)
			}
		})
	}
}

// TestCompactionFallbackTailTooLargeEndsWithoutMainRerun drives compactWithFallback
// on a cold main cache, so the fallback target runs first. The newest unit does not
// fit main's budget, so the attempt fails with compactionTailTooLargeError and the
// operation ends without a rerun on main.
func TestCompactionFallbackTailTooLargeEndsWithoutMainRerun(t *testing.T) {
	mainProv := &fakeProvider{}
	targetProv := &fakeProvider{}
	req := retentionFixtureRequest(3000, mainProv)
	req.CompactionFallback = CompactionFallback{
		After: time.Minute,
		Ref:   "anthropic/claude-small",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return compactionFallbackSummaryTarget(targetProv, prompt.ModelTokenBudget{ContextSize: 100000, NormalSummaryMaxTokens: 128}), nil
		},
	}
	messages := []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 assistant", ToolCalls: []ToolCall{{ID: "call-2", Name: "read"}}},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-2", Content: retentionHugeOutput},
	}
	state := RunState{Conversation: messages, Lineage: newConversationLineage(messages)}
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	var attempts []bool
	run := func(summaryReq *RunRequest, announce *compactionFallbackAnnouncement) (CompactionOutcome, error) {
		attempts = append(attempts, summaryReq != nil)
		// The stage pipeline twoStageSummarizeCompaction builds for each run.
		return summarizeCompactionStages{
			stageRunner: func(ctx context.Context, stageReq RunRequest, stageState RunState, turn int, stageCandidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
				params.announce = announce
				return summarizeCompactionStage(ctx, stageReq, stageState, turn, stageCandidate, params)
			},
			fitRunner:  fitConversationState,
			summaryReq: summaryReq,
		}.run(context.Background(), req, state, 1, candidate)
	}

	_, err := compactWithFallback(context.Background(), req, state, 1, run)
	var tooLarge *compactionTailTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("compactWithFallback() error = %v, want compactionTailTooLargeError", err)
	}
	if !reflect.DeepEqual(attempts, []bool{true}) {
		t.Fatalf("attempts (true = fallback) = %v, want one fallback attempt and no main rerun", attempts)
	}
	if got := len(mainProv.requests) + len(targetProv.requests); got != 0 {
		t.Errorf("provider calls = %d, want 0 when no tail fits", got)
	}
}

// This test sets the process-wide cold-start latch, so it must not run in
// parallel with other tests.
func TestCompactionFallbackCacheDiagnosticLeavesColdStart(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	mainReq := RunRequest{
		Diagnostics:   w,
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
	}
	fallbackReq := mainReq
	fallbackReq.compactionFallbackTarget = true
	fallbackReq.ResolvedModel = provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"}
	response := provider.ChatResponse{Usage: &provider.UsageStats{PromptTokens: 10}}

	emitCacheDiagnostic(fallbackReq, response, "compaction", 1, requestCacheStats{})
	if coldStartRecorded.Load() {
		t.Fatal("fallback emit set the cold-start latch, want it unset")
	}
	emitCacheDiagnostic(mainReq, response, "", 2, requestCacheStats{})
	if !coldStartRecorded.Load() {
		t.Fatal("main emit did not set the cold-start latch")
	}

	records := readCacheRecords(t, dir)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	wantColdStart := []bool{false, true}
	for i, rec := range records {
		var payload cachePayload
		if err := json.Unmarshal(mustMarshal(t, rec.Payload), &payload); err != nil {
			t.Fatalf("unmarshal record %d payload: %v", i, err)
		}
		if payload.ColdStart != wantColdStart[i] {
			t.Errorf("record %d ColdStart = %v, want %v", i, payload.ColdStart, wantColdStart[i])
		}
	}
}

// This test sets the process-wide cold-start latch, so it must not run in
// parallel with other tests.
func TestCompactionFallbackCacheDiagnosticKeepsSetLatch(t *testing.T) {
	resetColdStart(t)
	coldStartRecorded.Store(true)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	fallbackReq := RunRequest{
		Diagnostics:   w,
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
	}
	fallbackReq.compactionFallbackTarget = true
	response := provider.ChatResponse{Usage: &provider.UsageStats{PromptTokens: 10}}

	emitCacheDiagnostic(fallbackReq, response, "compaction", 1, requestCacheStats{})
	if !coldStartRecorded.Load() {
		t.Fatal("fallback emit cleared the cold-start latch, want it still set")
	}

	records := readCacheRecords(t, dir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	var payload cachePayload
	if err := json.Unmarshal(mustMarshal(t, records[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal record payload: %v", err)
	}
	if payload.ColdStart {
		t.Error("ColdStart = true, want false for a fallback record")
	}
}
