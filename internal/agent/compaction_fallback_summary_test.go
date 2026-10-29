package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
	targetProv := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false)}}
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

	outcome, err := summarizeCompactionStage(context.Background(), req, state, 1, candidate, compactionFallbackSummaryParams(maxTokens, &fallback))
	if err != nil {
		t.Fatalf("summarizeCompactionStage() error = %v", err)
	}
	if !outcome.Applied {
		t.Error("Applied = false, want true")
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

	outcome, err := summarizeCompactionStage(context.Background(), req, state, 1, candidate, compactionFallbackSummaryParams(128, &fallback))
	if !errors.Is(err, errCompactionFallbackNotFit) {
		t.Fatalf("error = %v, want errCompactionFallbackNotFit", err)
	}
	if outcome.Applied {
		t.Error("Applied = true, want false")
	}
	if got := len(targetProv.requests); got != 0 {
		t.Errorf("target provider requests = %d, want 0 when the summary does not fit", got)
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
			chatReq, _, _, err := buildCompactionRequestWithMode(context.Background(), tt.req, state, candidate, prompt.CompactionModeNormal, 128)
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
		})
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
