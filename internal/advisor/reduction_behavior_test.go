package advisor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestResolveReductionTuning(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		model       provider.ResolvedModel
		wantEnabled bool
		wantTrigger int
		wantTarget  int
		wantTail    int
	}{
		{
			name:        "zero disables",
			cfg:         Config{ContextReductionThresholdTokens: 0},
			model:       provider.ResolvedModel{EffectiveLimits: provider.EffectiveLimits{ContextWindow: 100000}},
			wantEnabled: false,
		},
		{
			name:        "configured trigger without known window",
			cfg:         Config{ContextReductionThresholdTokens: 10000},
			wantEnabled: true,
			wantTrigger: 10000,
			wantTarget:  7500,
			wantTail:    2500,
		},
		{
			name:        "window cap wins over larger configured trigger",
			cfg:         Config{ContextReductionThresholdTokens: 32000},
			model:       provider.ResolvedModel{EffectiveLimits: provider.EffectiveLimits{ContextWindow: 32000, MaxOutputTokens: 4096, EstimatorPadTokens: 320}},
			wantEnabled: true,
			wantTrigger: 16550,
			wantTarget:  12412,
			wantTail:    4138,
		},
		{
			name:        "configured output reserve lowers cap",
			cfg:         Config{ContextReductionThresholdTokens: 32000, MaxTokens: intPtr(8192)},
			model:       provider.ResolvedModel{EffectiveLimits: provider.EffectiveLimits{ContextWindow: 32000, MaxOutputTokens: 4096, EstimatorPadTokens: 320}},
			wantEnabled: true,
			wantTrigger: 14092,
			wantTarget:  10569,
			wantTail:    3523,
		},
		{
			name:        "configured trigger below cap is unchanged",
			cfg:         Config{ContextReductionThresholdTokens: 1000},
			model:       provider.ResolvedModel{EffectiveLimits: provider.EffectiveLimits{ContextWindow: 100000, MaxOutputTokens: 8192, EstimatorPadTokens: 1000}},
			wantEnabled: true,
			wantTrigger: 1000,
			wantTarget:  750,
			wantTail:    250,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveReductionTuning(tt.cfg, tt.model)
			if got.enabled != tt.wantEnabled || got.trigger != tt.wantTrigger || got.target != tt.wantTarget || got.tail != tt.wantTail {
				t.Fatalf("resolveReductionTuning() = %#v, want enabled=%v trigger=%d target=%d tail=%d", got, tt.wantEnabled, tt.wantTrigger, tt.wantTarget, tt.wantTail)
			}
		})
	}
}

func reductionTestPlan(transcript []provider.Message, trigger int) reductionPlan {
	return planReduction(reductionPlanInput{
		transcript:    transcript,
		stages:        make([]reductionStage, len(transcript)),
		tuning:        reductionTuning{enabled: true, trigger: trigger, target: 0, tail: 0},
		ratio:         0.25,
		initialTokens: 10000,
	})
}

func readResultMessage(t *testing.T, callID, path, output string, start, end, total int) provider.Message {
	t.Helper()
	data, err := json.Marshal(builtin.ReadResult{
		Path:       path,
		StartLine:  start,
		EndLine:    end,
		TotalLines: total,
		FileHash:   builtin.FileContentHash([]byte(output)),
		Output:     output,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return provider.Message{Role: provider.MessageRoleTool, Name: "read", ToolCallID: callID, Content: string(data)}
}

func assistantCall(callID, name string) provider.Message {
	return provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{ID: callID, Name: name}}}
}

func TestPlanReductionReadDedupe(t *testing.T) {
	large := strings.Repeat("line of text\n", 400)
	sameHashDifferentWhitespace := strings.Replace(large, "\n", "  \n", 1)
	if builtin.FileContentHash([]byte(large)) != builtin.FileContentHash([]byte(sameHashDifferentWhitespace)) {
		t.Fatal("test setup: hashes should be equal after trailing whitespace normalization")
	}

	tests := []struct {
		name       string
		messages   []provider.Message
		wantDedupe bool
	}{
		{
			name: "byte identical full reads dedupe later copy",
			messages: []provider.Message{
				assistantCall("r1", "read"), readResultMessage(t, "r1", "main.go", large, 1, 400, 400),
				assistantCall("r2", "read"), readResultMessage(t, "r2", "main.go", large, 1, 400, 400),
			},
			wantDedupe: true,
		},
		{
			name: "same normalized file hash but different bytes does not dedupe",
			messages: []provider.Message{
				assistantCall("r1", "read"), readResultMessage(t, "r1", "main.go", large, 1, 400, 400),
				assistantCall("r2", "read"), readResultMessage(t, "r2", "main.go", sameHashDifferentWhitespace, 1, 400, 400),
			},
			wantDedupe: false,
		},
		{
			name: "partial read does not dedupe",
			messages: []provider.Message{
				assistantCall("r1", "read"), readResultMessage(t, "r1", "main.go", large, 1, 400, 400),
				assistantCall("r2", "read"), readResultMessage(t, "r2", "main.go", large, 2, 400, 400),
			},
			wantDedupe: false,
		},
		{
			name: "paginated read does not dedupe",
			messages: []provider.Message{
				assistantCall("r1", "read"), readResultMessage(t, "r1", "main.go", large, 1, 400, 400),
				assistantCall("r2", "read"), {
					Role: provider.MessageRoleTool, Name: "read", ToolCallID: "r2",
					Content: `{"path":"main.go","start_line":1,"end_line":400,"total_lines":400,"file_hash":"x","next_offset":10,"output":"` + strings.ReplaceAll(large, "\n", "\\n") + `"}`,
				},
			},
			wantDedupe: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := planReduction(reductionPlanInput{
				transcript:    tt.messages,
				stages:        make([]reductionStage, len(tt.messages)),
				tuning:        reductionTuning{enabled: true, trigger: 1000, target: 9900, tail: 0},
				ratio:         0.25,
				initialTokens: 10000,
			})
			gotDedupe := false
			for _, stage := range plan.stages {
				if stage == reductionStageReadDedupe {
					gotDedupe = true
				}
			}
			if gotDedupe != tt.wantDedupe {
				t.Fatalf("read dedupe = %v, want %v; stages = %v", gotDedupe, tt.wantDedupe, plan.stages)
			}
			if tt.wantDedupe && !strings.Contains(plan.transcript[3].Content, "Same content as read call r1 earlier in this conversation") {
				t.Fatalf("dedupe note = %q, want earlier call reference", plan.transcript[3].Content)
			}
		})
	}
}

func TestClipOrdinaryToolContentPreservesMetadataAndUTF8(t *testing.T) {
	output := strings.Repeat("é", 3000)
	bash, err := json.Marshal(builtin.BashResult{
		FilePath: "artifact.log", ArtifactStatus: "complete", Message: "kept message", ExitCode: 3, Truncated: true, Output: output,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	clipped, ok := clipOrdinaryToolContent("bash", string(bash))
	if !ok {
		t.Fatal("clipOrdinaryToolContent() = false, want clipping")
	}
	var got builtin.BashResult
	if err := json.Unmarshal([]byte(clipped), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.FilePath != "artifact.log" || got.ArtifactStatus != "complete" || got.Message != "kept message" || got.ExitCode != 3 || !got.Truncated {
		t.Fatalf("metadata changed: %#v", got)
	}
	if !utf8.ValidString(got.Output) {
		t.Fatal("clipped output is not valid UTF-8")
	}
	if !strings.Contains(got.Output, "bytes omitted") {
		t.Fatalf("clipped output missing marker: %q", got.Output)
	}

	read, err := json.Marshal(builtin.ReadResult{
		Path: "main.go", StartLine: 1, EndLine: 10, TotalLines: 10, FileHash: "ABC", Output: strings.Repeat("x", 5000),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	clippedRead, ok := clipOrdinaryToolContent("read", string(read))
	if !ok {
		t.Fatal("clipOrdinaryToolContent(read) = false, want clipping")
	}
	var gotRead builtin.ReadResult
	if err := json.Unmarshal([]byte(clippedRead), &gotRead); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if gotRead.Path != "main.go" || gotRead.TotalLines != 10 || gotRead.FileHash != "ABC" {
		t.Fatalf("read metadata changed: %#v", gotRead)
	}
	if len(gotRead.Output) >= len(strings.Repeat("x", 5000)) {
		t.Fatal("read output was not clipped")
	}
}

func TestPlanReductionProtectsDelegationUserSystemAndAssistant(t *testing.T) {
	huge := strings.Repeat("delegation result\n", 500)
	big := strings.Repeat("ordinary output\n", 500)
	transcript := []provider.Message{
		{Role: provider.MessageRoleSystem, Content: "system prompt"},
		{Role: provider.MessageRoleUser, Content: huge},
		{Role: provider.MessageRoleAssistant, Content: "prose", ToolCalls: []provider.ToolCall{
			{ID: "d1", Name: "sub_agent"}, {ID: "d2", Name: "follow_up"}, {ID: "d3", Name: "sub_agent"},
			{ID: "a1", Name: "advisor"}, {ID: "a2", Name: "advisor"},
		}},
		{Role: provider.MessageRoleTool, Name: "sub_agent", ToolCallID: "d1", Content: huge},
		{Role: provider.MessageRoleTool, Name: "", ToolCallID: "d2", Content: huge},
		{Role: provider.MessageRoleTool, Name: "read", ToolCallID: "d3", Content: huge},
		{Role: provider.MessageRoleTool, Name: "", ToolCallID: "", Content: huge},
		{Role: provider.MessageRoleTool, Name: "advisor", ToolCallID: "a1", Content: huge},
		{Role: provider.MessageRoleTool, Name: "read", ToolCallID: "a2", Content: huge},
		{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b1", Content: func() string {
			data, err := json.Marshal(builtin.BashResult{ArtifactStatus: "none", Output: big})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			return string(data)
		}()},
	}
	plan := reductionTestPlan(transcript, 1000)
	for i := 0; i <= 8; i++ {
		if plan.transcript[i].Content != transcript[i].Content {
			t.Fatalf("protected message %d changed: %q", i, plan.transcript[i].Content)
		}
	}
	if plan.stages[9] == reductionStageNone {
		t.Fatal("ordinary bash result was not reduced")
	}
}

func TestPlanReductionProtectedOnlyHasNoSavings(t *testing.T) {
	huge := strings.Repeat("protected output\n", 500)
	transcript := []provider.Message{
		{Role: provider.MessageRoleTool, Name: "sub_agent", ToolCallID: "d1", Content: huge},
		{Role: provider.MessageRoleTool, Name: "follow_up", ToolCallID: "d2", Content: huge},
		{Role: provider.MessageRoleTool, Name: "advisor", ToolCallID: "a1", Content: huge},
	}
	plan := reductionTestPlan(transcript, 1000)
	if plan.savedTokens != 0 || len(plan.appliedStages) != 0 {
		t.Fatalf("protected-only plan saved %d tokens with stages %v, want none", plan.savedTokens, plan.appliedStages)
	}
}

func TestReductionLedgerReplayAppendDivergenceAndInFlight(t *testing.T) {
	transcript := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "first"},
		{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b1", Content: "long output"},
	}
	replay := replayReductionLedger(transcript, nil)
	entries := []reductionLedgerEntry{
		{sourceHash: replay.hashes[0]},
		{sourceHash: replay.hashes[1], replacement: "stub", stage: reductionStageStub},
	}
	replayed := replayReductionLedger(transcript, entries)
	if !replayed.changed || replayed.transcript[1].Content != "stub" || replayed.valid != 2 {
		t.Fatalf("initial replay = %#v, want frozen stub", replayed)
	}

	appended := append(append([]provider.Message(nil), transcript...), provider.Message{Role: provider.MessageRoleUser, Content: "later"})
	replayed = replayReductionLedger(appended, entries)
	if replayed.valid != 2 || replayed.transcript[1].Content != "stub" || replayed.transcript[2].Content != "later" {
		t.Fatalf("append replay = %#v, want prefix unchanged", replayed)
	}

	changedFirst := append([]provider.Message(nil), transcript...)
	changedFirst[0].Content = "compacted"
	replayed = replayReductionLedger(changedFirst, entries)
	if replayed.valid != 0 || replayed.changed {
		t.Fatalf("index0 divergence = %#v, want cleared ledger", replayed)
	}

	changedSecond := append([]provider.Message(nil), transcript...)
	changedSecond[1].Content = "different"
	replayed = replayReductionLedger(changedSecond, entries)
	if replayed.valid != 1 || replayed.changed {
		t.Fatalf("later divergence = %#v, want only prefix valid", replayed)
	}

	transcriptWithInflight := append(append([]provider.Message(nil), transcript...), provider.Message{Role: provider.MessageRoleAssistant, Content: "in flight"})
	base, inFlight := splitTranscript(transcriptWithInflight)
	if inFlight == nil || inFlight.Content != "in flight" || len(base) != len(transcript) {
		t.Fatalf("splitTranscript() = %#v, %#v", base, inFlight)
	}
	replayed = replayReductionLedger(base, entries)
	if replayed.valid != 2 || replayed.transcript[1].Content != "stub" {
		t.Fatalf("in-flight assistant affected replay: %#v", replayed)
	}
}

func TestSharedStateReductionScopeIsolationAndLRU(t *testing.T) {
	state := NewSharedState()
	for i := 0; i < maxReductionScopes+2; i++ {
		scope := string(rune('a' + i))
		if !state.storeReductionLedger(scope, 0, reductionLedgerState{entries: []reductionLedgerEntry{{sourceHash: scope}}}) {
			t.Fatalf("storeReductionLedger(%q) = false", scope)
		}
	}
	if got := state.reductionLedger("a"); len(got.entries) != 0 {
		t.Fatalf("oldest scope survived LRU eviction: %#v", got)
	}
	if got := state.reductionLedger("h"); len(got.entries) != 1 {
		t.Fatalf("newest scope missing: %#v", got)
	}
	if state.storeReductionLedger("j", 0, reductionLedgerState{}) {
		t.Fatal("stale epoch store succeeded, want CAS failure")
	}
}

func TestSharedStateReductionLedgerConcurrentAccess(t *testing.T) {
	state := NewSharedState()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := "scope"
			if i%2 == 0 {
				scope = "other"
			}
			for j := 0; j < 100; j++ {
				view := state.reductionLedger(scope)
				state.storeReductionLedger(scope, view.epoch, reductionLedgerState{entries: []reductionLedgerEntry{{sourceHash: scope}}})
			}
		}(i)
	}
	wg.Wait()
}

func bigBashMessage(callID string, bytes int) provider.Message {
	data, _ := json.Marshal(builtin.BashResult{ArtifactStatus: "none", Output: strings.Repeat("z", bytes)})
	return provider.Message{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: callID, Content: string(data)}
}

func reductionSnapshot() []provider.Message {
	return []provider.Message{
		{Role: provider.MessageRoleUser, Content: "run the checks"},
		assistantCall("b1", "bash"),
		bigBashMessage("b1", 8000),
		assistantCall("b2", "bash"),
		bigBashMessage("b2", 8000),
	}
}

func TestHandlerReductionBelowAndAtTrigger(t *testing.T) {
	snapshot := reductionSnapshot()
	deps := HandlerDeps{Model: provider.ResolvedModel{BackendModelID: "advisor-model"}}
	baseline := buildMessages(snapshot, "", nil)
	initial := estimateAdvisorMessages(context.Background(), deps, baseline)
	if initial.tokens <= 0 {
		t.Fatal("initial estimate = 0, want positive")
	}

	t.Run("below trigger is legacy", func(t *testing.T) {
		prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
		handler := NewHandler(HandlerDeps{Provider: prov, Model: deps.Model, Config: Config{MaxUsesPerRun: 1, ContextReductionThresholdTokens: initial.tokens + 1}})
		ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
		if _, err := handler(ctx, nil); err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if len(prov.requests) != 1 || !messagesEqual(prov.requests[0].Messages, baseline) {
			t.Fatal("below-trigger request changed from legacy payload")
		}
	})

	t.Run("at trigger reduces", func(t *testing.T) {
		prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
		handler := NewHandler(HandlerDeps{Provider: prov, Model: deps.Model, Config: Config{MaxUsesPerRun: 1, ContextReductionThresholdTokens: initial.tokens}})
		ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
		if _, err := handler(ctx, nil); err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if len(prov.requests) != 1 {
			t.Fatalf("provider calls = %d, want 1", len(prov.requests))
		}
		if messagesEqual(prov.requests[0].Messages, baseline) {
			t.Fatal("at-trigger request equal to legacy payload, want reduction")
		}
		if !messagesContain(prov.requests[0].Messages, "omitted") {
			t.Fatal("at-trigger request missing reduction marker")
		}
	})
}

func messagesEqual(a, b []provider.Message) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func messagesContain(messages []provider.Message, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func TestHandlerReductionReplaysAcrossAppend(t *testing.T) {
	snapshot := reductionSnapshot()
	deps := HandlerDeps{Model: provider.ResolvedModel{BackendModelID: "advisor-model"}}
	initial := estimateAdvisorMessages(context.Background(), deps, buildMessages(snapshot, "", nil))
	shared := NewSharedState()
	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	handler := NewHandler(HandlerDeps{Provider: prov, Model: deps.Model, Config: Config{MaxUsesPerRun: 3, ContextReductionThresholdTokens: initial.tokens}, SharedState: shared})
	ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("first handler() error = %v", err)
	}
	first := prov.requests[0].Messages

	appended := append(append([]provider.Message(nil), snapshot...), provider.Message{Role: provider.MessageRoleUser, Content: "continue"})
	ctx = agent.WithConversationSnapshot(context.Background(), appended)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("second handler() error = %v", err)
	}
	second := prov.requests[1].Messages
	if len(second) <= len(first)-1 {
		t.Fatalf("second request length = %d, want more than %d", len(second), len(first)-1)
	}
	if !messagesEqual(first[:len(first)-1], second[:len(first)-1]) {
		t.Fatal("appended conversation did not replay the frozen prefix exactly")
	}
	if !messagesContain(second, "omitted") {
		t.Fatal("second request lost the frozen reduction marker")
	}
}

func failingTokenCounter() provider.TokenCounter {
	return failingEstimateCounter{}
}

type failingEstimateCounter struct{}

func (failingEstimateCounter) EstimateChatRequestTokens(context.Context, provider.ChatRequest) (provider.TokenEstimate, error) {
	return provider.TokenEstimate{}, errors.New("estimator unavailable")
}

func (failingEstimateCounter) ObserveUsage(context.Context, provider.ChatRequest, *provider.UsageStats) {
}

func TestHandlerEstimatorFallbackStillCallsProvider(t *testing.T) {
	restore := provider.SwapDefaultTokenCounter(failingTokenCounter())
	defer restore()

	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 1, ContextReductionThresholdTokens: 50},
	})
	snapshot := reductionSnapshot()
	ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	if !messagesContain(prov.requests[0].Messages, "omitted") {
		t.Fatal("fallback estimate did not trigger reduction")
	}
	var reduction *output.AdvisorReductionInfo
	for _, event := range sink.events {
		if complete, ok := event.Payload.(output.AdvisorCompleteEvent); ok {
			reduction = complete.Reduction
		}
	}
	if reduction == nil || reduction.Estimator != "fallback" {
		t.Fatalf("reduction diagnostics = %#v, want fallback estimator", reduction)
	}
}

func TestHandlerExhaustionAndGrowthReactivation(t *testing.T) {
	first := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "run it"},
		assistantCall("b1", "bash"),
		bigBashMessage("b1", 8000),
	}
	deps := HandlerDeps{Model: provider.ResolvedModel{BackendModelID: "advisor-model"}}
	initial := estimateAdvisorMessages(context.Background(), deps, buildMessages(first, "", nil))
	shared := NewSharedState()
	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	handler := NewHandler(HandlerDeps{Provider: prov, Model: deps.Model, Config: Config{MaxUsesPerRun: 5, ContextReductionThresholdTokens: initial.tokens}, SharedState: shared})

	ctx := agent.WithConversationSnapshot(context.Background(), first)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("first handler() error = %v", err)
	}
	firstMessages := prov.requests[0].Messages
	if messagesContain(firstMessages, "omitted") {
		t.Fatal("protected-only request was reduced, want exhaustion without changes")
	}

	ctx = agent.WithConversationSnapshot(context.Background(), first)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("second handler() error = %v", err)
	}
	if !messagesEqual(prov.requests[1].Messages, firstMessages) {
		t.Fatal("exhausted request churned under the growth threshold")
	}

	grown := append(append([]provider.Message(nil), first...), assistantCall("b2", "bash"), bigBashMessage("b2", 8000))
	ctx = agent.WithConversationSnapshot(context.Background(), grown)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("third handler() error = %v", err)
	}
	if !messagesContain(prov.requests[2].Messages, "omitted") {
		t.Fatal("grown request did not reactivate reduction")
	}
}
