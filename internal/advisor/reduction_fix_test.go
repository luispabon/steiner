package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestPlanReductionStructuredStagesPreserveMetadata(t *testing.T) {
	output := strings.Repeat("payload line\n", 900)
	read := func(callID string) provider.Message {
		data, err := json.Marshal(builtin.ReadResult{
			Path: "main.go", StartLine: 1, EndLine: 900, TotalLines: 900, FileHash: "ABC123", Output: output,
		})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return provider.Message{Role: provider.MessageRoleTool, Name: "read", ToolCallID: callID, Content: string(data)}
	}

	t.Run("dedupe note", func(t *testing.T) {
		transcript := []provider.Message{assistantCall("r1", "read"), read("r1"), assistantCall("r2", "read"), read("r2")}
		plan := planReduction(reductionPlanInput{
			transcript: transcript, stages: make([]reductionStage, len(transcript)),
			tuning: reductionTuning{enabled: true, trigger: 1000, target: 9900, tail: 0},
			ratio:  0.25, initialTokens: 10000,
		})
		if plan.stages[3] != reductionStageReadDedupe {
			t.Fatalf("stage = %v, want read dedupe", plan.stages[3])
		}
		var got builtin.ReadResult
		if err := json.Unmarshal([]byte(plan.transcript[3].Content), &got); err != nil {
			t.Fatalf("dedupe note is not structured: %v (%q)", err, plan.transcript[3].Content)
		}
		if got.Path != "main.go" || got.StartLine != 1 || got.EndLine != 900 || got.TotalLines != 900 || got.FileHash != "ABC123" {
			t.Fatalf("dedupe note metadata changed: %#v", got)
		}
		if !strings.Contains(got.Output, "Same content as read call r1") {
			t.Fatalf("dedupe note output = %q, want earlier call reference", got.Output)
		}
	})

	t.Run("clip and stub", func(t *testing.T) {
		transcript := []provider.Message{assistantCall("b1", "bash"), {
			Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b1",
			Content: func() string {
				data, err := json.Marshal(builtin.BashResult{FilePath: "out.log", ArtifactStatus: "complete", Message: "kept", ExitCode: 2, Output: output})
				if err != nil {
					t.Fatalf("json.Marshal() error = %v", err)
				}
				return string(data)
			}(),
		}}
		plan := planReduction(reductionPlanInput{
			transcript: transcript, stages: make([]reductionStage, len(transcript)),
			tuning: reductionTuning{enabled: true, trigger: 1000, target: 0, tail: 0},
			ratio:  0.25, initialTokens: 10000,
		})
		if plan.stages[1] != reductionStageStub {
			t.Fatalf("stage = %v, want stub", plan.stages[1])
		}
		var got builtin.BashResult
		if err := json.Unmarshal([]byte(plan.transcript[1].Content), &got); err != nil {
			t.Fatalf("stub is not structured: %v (%q)", err, plan.transcript[1].Content)
		}
		if got.FilePath != "out.log" || got.ArtifactStatus != "complete" || got.Message != "kept" || got.ExitCode != 2 {
			t.Fatalf("stub metadata changed: %#v", got)
		}
		if !strings.Contains(got.Output, "output omitted") {
			t.Fatalf("stub output = %q, want omission marker", got.Output)
		}
	})

	t.Run("reconcile reduced source", func(t *testing.T) {
		transcript := []provider.Message{assistantCall("r1", "read"), read("r1"), assistantCall("r2", "read"), read("r2")}
		plan := planReduction(reductionPlanInput{
			transcript: transcript, stages: make([]reductionStage, len(transcript)),
			tuning: reductionTuning{enabled: true, trigger: 1000, target: 0, tail: 0},
			ratio:  0.25, initialTokens: 10000,
		})
		if plan.stages[3] != reductionStageStub || plan.references[3] != -1 {
			t.Fatalf("reconciled note stage/reference = %v/%d, want stub/-1", plan.stages[3], plan.references[3])
		}
		var got builtin.ReadResult
		if err := json.Unmarshal([]byte(plan.transcript[3].Content), &got); err != nil {
			t.Fatalf("reconciled note is not structured: %v (%q)", err, plan.transcript[3].Content)
		}
		if got.Output != unavailableDuplicateStub {
			t.Fatalf("reconciled output = %q, want self-contained stub", got.Output)
		}
		if got.Path != "main.go" || got.FileHash != "ABC123" || got.TotalLines != 900 {
			t.Fatalf("reconciled metadata changed: %#v", got)
		}
	})

	t.Run("malformed structured result is left alone", func(t *testing.T) {
		raw := strings.Repeat("not json\n", 900)
		transcript := []provider.Message{{Role: provider.MessageRoleTool, Name: "read", ToolCallID: "r1", Content: raw}}
		plan := planReduction(reductionPlanInput{
			transcript: transcript, stages: make([]reductionStage, len(transcript)),
			tuning: reductionTuning{enabled: true, trigger: 1000, target: 0, tail: 0},
			ratio:  0.25, initialTokens: 10000,
		})
		if plan.stages[0] != reductionStageNone || plan.transcript[0].Content != raw {
			t.Fatalf("malformed read was reduced as whole JSON: stage=%v content=%q", plan.stages[0], plan.transcript[0].Content)
		}
	})
}

func TestSharedStateAbsentReductionScopeDoesNotEvict(t *testing.T) {
	state := NewSharedState()
	for i := 0; i < maxReductionScopes; i++ {
		scope := fmt.Sprintf("live-%d", i)
		if !state.storeReductionLedger(scope, 0, reductionLedgerState{entries: []reductionLedgerEntry{{sourceHash: scope}}}) {
			t.Fatalf("storeReductionLedger(%q) = false", scope)
		}
	}
	for i := 0; i < maxReductionScopes; i++ {
		if got := state.reductionLedger(fmt.Sprintf("missing-%d", i)); len(got.entries) != 0 {
			t.Fatalf("missing scope returned entries: %#v", got)
		}
	}
	for i := 0; i < maxReductionScopes; i++ {
		scope := fmt.Sprintf("live-%d", i)
		if got := state.reductionLedger(scope); len(got.entries) != 1 {
			t.Fatalf("live scope %q evicted by absent lookups: %#v", scope, got)
		}
		if _, ok := state.reductions[scope]; !ok {
			t.Fatalf("live scope %q missing from store", scope)
		}
	}
	if _, ok := state.reductions["missing-0"]; ok {
		t.Fatal("absent scope was inserted into the store")
	}
}

func TestDedupeAttachmentsUsesExactBytes(t *testing.T) {
	workDir := t.TempDir()
	content := "package main\n\nfunc main() {}\n"
	whitespaceVariant := strings.Replace(content, "\n", "  \n", 1)
	if builtin.FileContentHash([]byte(content)) != builtin.FileContentHash([]byte(whitespaceVariant)) {
		t.Fatal("test setup: variants should share a normalized file hash")
	}
	message := func(output string, start, end, total int) provider.Message {
		data, err := json.Marshal(builtin.ReadResult{
			Path: "main.go", StartLine: start, EndLine: end, TotalLines: total,
			FileHash: builtin.FileContentHash([]byte(output)), Output: output,
		})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return provider.Message{Role: provider.MessageRoleTool, Name: "read", ToolCallID: "r1", Content: string(data)}
	}
	files := []advisorFile{{DisplayPath: "main.go", Content: content, TotalBytes: len(content)}}

	cases := []struct {
		name        string
		transcript  []provider.Message
		stages      []reductionStage
		wantDeduped bool
	}{
		{
			name:        "exact bytes dedupe",
			transcript:  []provider.Message{message(content, 1, 3, 3)},
			stages:      make([]reductionStage, 1),
			wantDeduped: true,
		},
		{
			name:       "same hash different bytes does not dedupe",
			transcript: []provider.Message{message(whitespaceVariant, 1, 3, 3)},
			stages:     make([]reductionStage, 1),
		},
		{
			name:       "line numbered output does not dedupe",
			transcript: []provider.Message{message("     1│package main\n", 1, 1, 1)},
			stages:     make([]reductionStage, 1),
		},
		{
			name:       "partial read does not dedupe",
			transcript: []provider.Message{message(content, 2, 3, 3)},
			stages:     make([]reductionStage, 1),
		},
		{
			name:       "reduced read does not dedupe",
			transcript: []provider.Message{message(content, 1, 3, 3)},
			stages:     []reductionStage{reductionStageClip},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupeAttachments(files, tt.transcript, tt.stages, workDir)
			if got[0].Deduped != tt.wantDeduped {
				t.Fatalf("Deduped = %v, want %v", got[0].Deduped, tt.wantDeduped)
			}
			if !tt.wantDeduped && got[0].Content != content {
				t.Fatalf("content = %q, want untouched", got[0].Content)
			}
		})
	}
}

func TestFallbackRequestTokensArithmetic(t *testing.T) {
	tests := []struct {
		name     string
		messages []provider.Message
		want     int
	}{
		{
			name:     "plain text",
			messages: []provider.Message{{Role: provider.MessageRoleSystem, Content: "abcd"}},
			want:     4,
		},
		{
			name: "image payload",
			messages: []provider.Message{{
				Role:   provider.MessageRoleUser,
				Images: []provider.ImageBlock{{MediaType: "image/png", Data: "abcd"}},
			}},
			want: 34,
		},
		{
			name: "tool arguments",
			messages: []provider.Message{{
				Role:      provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{ID: "i", Name: "n", Arguments: map[string]any{"a": "b"}}},
			}},
			want: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fallbackRequestTokens(tt.messages); got != tt.want {
				t.Fatalf("fallbackRequestTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHandlerConcurrentReductionPlanCAS(t *testing.T) {
	snapshot := reductionSnapshot()
	baseDeps := HandlerDeps{Model: provider.ResolvedModel{BackendModelID: "advisor-model"}, TransportSession: "main-advisor"}
	initial := estimateAdvisorMessages(context.Background(), baseDeps, buildMessages(snapshot, "", nil))
	cfg := Config{MaxUsesPerRun: 32, ContextReductionThresholdTokens: initial.tokens}
	shared := NewSharedState()
	const workers = 8
	providers := make([]*fakeProvider, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
			providers[i] = prov
			handler := NewHandler(HandlerDeps{
				Provider: prov, Model: baseDeps.Model, Config: cfg, SharedState: shared,
				CacheKey: "shared", TransportSession: "main-advisor",
			})
			ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
			if _, err := handler(ctx, nil); err != nil {
				t.Errorf("handler() error = %v", err)
			}
		}(i)
	}
	wg.Wait()
	for i, prov := range providers {
		if prov == nil || len(prov.requests) != 1 {
			t.Fatalf("provider %d requests = %v, want one", i, prov)
		}
	}
	tuning := resolveReductionTuning(cfg, baseDeps.Model)
	scope := reductionScopeKey(baseDeps, "shared", tuning)
	state := shared.reductionLedger(scope)
	if state.epoch == 0 {
		t.Fatal("no concurrent plan committed")
	}
	if len(state.entries) != len(snapshot) {
		t.Fatalf("ledger entries = %d, want %d", len(state.entries), len(snapshot))
	}
	if replay := replayReductionLedger(snapshot, state.entries); replay.valid != len(snapshot) {
		t.Fatalf("replay valid = %d, want %d", replay.valid, len(snapshot))
	}
}
