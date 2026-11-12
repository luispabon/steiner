package advisor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func probeDeps() HandlerDeps {
	return HandlerDeps{
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		CacheKey: "exhaustion-probe",
	}
}

func probeTokens(t *testing.T, deps HandlerDeps, snapshot []provider.Message) int {
	t.Helper()
	_, messages := legacyReductionPayload(deps, reductionRequest{snapshot: snapshot})
	return estimateAdvisorMessages(context.Background(), deps, messages).tokens
}

const probeResultBytes = 6000

func probeMutationMessage(t *testing.T, callID string) provider.Message {
	t.Helper()
	result := func(pad string) ([]byte, error) {
		return json.Marshal(builtin.MutateResult{
			Paths:             []string{"internal/advisor/reduction.go"},
			Modified:          []string{"internal/advisor/reduction.go"},
			FileHashes:        map[string]string{"internal/advisor/reduction.go": "0123456789ABCDEF"},
			OperationsApplied: 1,
			Output:            pad,
		})
	}
	base, err := result("")
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	pad := probeResultBytes - len(base)
	if pad < 0 {
		t.Fatalf("target %d shorter than mutation base %d", probeResultBytes, len(base))
	}
	data, err := result(strings.Repeat("x", pad))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	// Output is omitempty, so the padded payload also gains the field itself.
	// Adjust once by the exact difference; each added byte is one 'x'.
	if diff := probeResultBytes - len(data); diff != 0 {
		pad += diff
		if pad < 0 {
			t.Fatalf("target %d shorter than mutation base %d", probeResultBytes, len(base))
		}
		data, err = result(strings.Repeat("x", pad))
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
	}
	if len(data) != probeResultBytes {
		t.Fatalf("mutation message length = %d, want %d", len(data), probeResultBytes)
	}
	return provider.Message{Role: provider.MessageRoleTool, Name: "mutate", ToolCallID: callID, Content: string(data)}
}

func probeBashMessage(t *testing.T, callID string) provider.Message {
	t.Helper()
	base, err := json.Marshal(builtin.BashResult{ArtifactStatus: "none"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	pad := probeResultBytes - len(base)
	if pad < 0 {
		t.Fatalf("target %d shorter than bash base %d", probeResultBytes, len(base))
	}
	data, err := json.Marshal(builtin.BashResult{ArtifactStatus: "none", Output: strings.Repeat("y", pad)})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if len(data) != probeResultBytes {
		t.Fatalf("bash message length = %d, want %d", len(data), probeResultBytes)
	}
	return provider.Message{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: callID, Content: string(data)}
}

func newProbeHandler(t *testing.T, deps HandlerDeps, cfg Config, shared *SharedState, prov *fakeProvider) (func(context.Context, map[string]any) (any, error), *toolSink) {
	t.Helper()
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov, Model: deps.Model, Config: cfg, SharedState: shared, CacheKey: deps.CacheKey, Events: sink,
	})
	return handler, sink
}

// probeReductionStrategy returns the strategy and stage count of the most
// recent advisor complete event, so tests assert the reduction outcome instead
// of matching text that caller input could also contain.
func probeReductionStrategy(t *testing.T, sink *toolSink) (string, int) {
	t.Helper()
	for i := len(sink.events) - 1; i >= 0; i-- {
		complete, ok := sink.events[i].Payload.(output.AdvisorCompleteEvent)
		if !ok {
			continue
		}
		if complete.Reduction == nil {
			return "", 0
		}
		return complete.Reduction.Strategy, len(complete.Reduction.Stages)
	}
	t.Fatal("no advisor complete event")
	return "", 0
}

func runProbe(t *testing.T, handler func(context.Context, map[string]any) (any, error), snapshot []provider.Message) {
	t.Helper()
	ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
}

func probeLedger(t *testing.T, deps HandlerDeps, cfg Config, shared *SharedState) reductionLedgerState {
	t.Helper()
	return shared.reductionLedger(reductionScopeKey(deps, deps.CacheKey, resolveReductionTuning(cfg, deps.Model)))
}

// TestHandlerExhaustionDoesNotSurviveInteriorReplacement proves that an
// exhausted transcript is replanned when an interior message is replaced,
// even when the replacement keeps every message length identical.
func TestHandlerExhaustionDoesNotSurviveInteriorReplacement(t *testing.T) {
	deps := probeDeps()
	user := provider.Message{Role: provider.MessageRoleUser, Content: "run the checks"}
	first := []provider.Message{
		user,
		probeMutationMessage(t, "m1"),
		probeMutationMessage(t, "m2"),
	}
	cfg := Config{MaxUsesPerRun: 2, ContextReductionThresholdTokens: probeTokens(t, deps, first)}
	shared := NewSharedState()
	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	handler, sink := newProbeHandler(t, deps, cfg, shared, prov)

	runProbe(t, handler, first)
	if strategy, _ := probeReductionStrategy(t, sink); strategy != "exhausted" {
		t.Fatalf("first request strategy = %q, want exhausted", strategy)
	}
	if !probeLedger(t, deps, cfg, shared).exhausted {
		t.Fatal("first request did not record exhaustion")
	}

	replaced := []provider.Message{
		user,
		probeBashMessage(t, "b1"),
		probeBashMessage(t, "b2"),
	}
	for i := range first {
		if len(first[i].Content) != len(replaced[i].Content) {
			t.Fatalf("message %d length changed: %d vs %d", i, len(first[i].Content), len(replaced[i].Content))
		}
	}
	runProbe(t, handler, replaced)
	if strategy, stages := probeReductionStrategy(t, sink); strategy != "reduced" || stages == 0 {
		t.Fatalf("interior replacement strategy = %q with %d stages, want reduced", strategy, stages)
	}
}

// TestHandlerExhaustionDoesNotSurviveShrinkOrRegrow proves that a shrunk
// conversation stops matching the exhausted prefix and that a later regrow is
// planned normally.
func TestHandlerExhaustionDoesNotSurviveShrinkOrRegrow(t *testing.T) {
	deps := probeDeps()
	user := provider.Message{Role: provider.MessageRoleUser, Content: strings.Repeat("context line\n", 500)}
	shrink := []provider.Message{user}
	first := []provider.Message{user, probeMutationMessage(t, "m1")}
	cfg := Config{MaxUsesPerRun: 3, ContextReductionThresholdTokens: probeTokens(t, deps, shrink)}
	shared := NewSharedState()
	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	handler, sink := newProbeHandler(t, deps, cfg, shared, prov)

	runProbe(t, handler, first)
	if strategy, _ := probeReductionStrategy(t, sink); strategy != "exhausted" {
		t.Fatalf("first request strategy = %q, want exhausted", strategy)
	}
	state := probeLedger(t, deps, cfg, shared)
	if !state.exhausted || state.exhaustedLen != len(first) {
		t.Fatalf("first request ledger = %#v, want exhaustion at length %d", state, len(first))
	}

	runProbe(t, handler, shrink)
	if strategy, _ := probeReductionStrategy(t, sink); strategy != "exhausted" {
		t.Fatalf("shrink strategy = %q, want exhausted", strategy)
	}
	state = probeLedger(t, deps, cfg, shared)
	wantTokens := probeTokens(t, deps, shrink)
	if !state.exhausted || state.exhaustedLen != len(shrink) || state.exhaustedTokens != wantTokens {
		t.Fatalf("shrink ledger = %#v, want exhaustion at length %d and %d tokens", state, len(shrink), wantTokens)
	}

	regrown := []provider.Message{
		user,
		probeBashMessage(t, "b1"),
		probeBashMessage(t, "b2"),
	}
	runProbe(t, handler, regrown)
	if strategy, stages := probeReductionStrategy(t, sink); strategy != "reduced" || stages == 0 {
		t.Fatalf("regrown strategy = %q with %d stages, want reduced", strategy, stages)
	}
}

// TestHandlerExhaustionSuppressesTrueAppendAndIgnoresInFlight proves that a
// true append keeps suppression without rewriting the exhausted state, and
// that the trailing in-flight assistant message is not part of the identity.
func TestHandlerExhaustionSuppressesTrueAppendAndIgnoresInFlight(t *testing.T) {
	deps := probeDeps()
	first := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "run the checks"},
		probeMutationMessage(t, "m1"),
	}
	cfg := Config{MaxUsesPerRun: 3, ContextReductionThresholdTokens: probeTokens(t, deps, first)}
	shared := NewSharedState()
	prov := &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}}}
	handler, sink := newProbeHandler(t, deps, cfg, shared, prov)

	runProbe(t, handler, first)
	before := probeLedger(t, deps, cfg, shared)
	if !before.exhausted {
		t.Fatal("first request did not record exhaustion")
	}

	appended := append(append([]provider.Message(nil), first...), provider.Message{Role: provider.MessageRoleUser, Content: "ok"})
	runProbe(t, handler, appended)
	if strategy, _ := probeReductionStrategy(t, sink); strategy != "exhausted" {
		t.Fatalf("small append strategy = %q, want exhausted", strategy)
	}
	after := probeLedger(t, deps, cfg, shared)
	if !after.exhausted || after.exhaustedLen != before.exhaustedLen || after.exhaustedTokens != before.exhaustedTokens {
		t.Fatalf("append rewrote exhaustion: before %#v after %#v", before, after)
	}

	withInFlight := append(append([]provider.Message(nil), first...), provider.Message{
		Role:      provider.MessageRoleAssistant,
		Content:   "checking the notes",
		ToolCalls: []provider.ToolCall{{ID: "advisor-1", Name: ToolName}},
	})
	runProbe(t, handler, withInFlight)
	if strategy, _ := probeReductionStrategy(t, sink); strategy != "exhausted" {
		t.Fatalf("in-flight strategy = %q, want exhausted", strategy)
	}
	afterInflight := probeLedger(t, deps, cfg, shared)
	if !afterInflight.exhausted || afterInflight.exhaustedLen != before.exhaustedLen || afterInflight.exhaustedTokens != before.exhaustedTokens {
		t.Fatalf("in-flight assistant rewrote exhaustion: before %#v after %#v", before, afterInflight)
	}
}

func TestReplayHashesLazilyAndKeepsSourceImmutable(t *testing.T) {
	long := strings.Repeat("x", 64)
	source := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "first"},
		{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b1", Content: long},
		{Role: provider.MessageRoleUser, Content: "third"},
	}
	identity := replayReductionLedger(source, nil)
	if len(identity.hashes) != 0 {
		t.Fatalf("empty ledger hashed %d messages, want none", len(identity.hashes))
	}
	hashes := identity.sourceHashes()
	if len(hashes) != len(source) {
		t.Fatalf("sourceHashes() length = %d, want %d", len(hashes), len(source))
	}

	mismatched := replayReductionLedger(source, []reductionLedgerEntry{
		{sourceHash: hashes[0]},
		{sourceHash: "mismatch"},
		{sourceHash: hashes[2]},
	})
	if mismatched.valid != 1 {
		t.Fatalf("valid = %d, want 1", mismatched.valid)
	}
	if len(mismatched.hashes) != 2 {
		t.Fatalf("prefix mismatch hashed %d messages, want 2", len(mismatched.hashes))
	}
	mismatched.sourceHashes()
	if len(mismatched.hashes) != len(source) {
		t.Fatalf("sourceHashes() filled to %d, want %d", len(mismatched.hashes), len(source))
	}
	if source[1].Content != long {
		t.Fatal("lazy replay mutated the source message")
	}

	applied := replayReductionLedger(source, []reductionLedgerEntry{
		{sourceHash: hashes[0]},
		{sourceHash: hashes[1], replacement: "stub", stage: reductionStageStub, referenceIndex: -1},
		{sourceHash: hashes[2]},
	})
	if applied.valid != len(source) || applied.transcript[1].Content != "stub" || applied.stages[1] != reductionStageStub {
		t.Fatalf("applied replay = %#v, want frozen stub", applied)
	}
	if source[1].Content != long {
		t.Fatal("replacement replay mutated the source message")
	}
	if &applied.transcript[0] == &source[0] {
		t.Fatal("replacement replay aliased the source transcript")
	}
}

func TestExhaustionCoversTranscriptRequiresExactPrefix(t *testing.T) {
	source := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "one"},
		{Role: provider.MessageRoleUser, Content: "two"},
	}
	base := replayReductionLedger(source, nil)
	state := reductionLedgerState{exhausted: true, exhaustedLen: len(source), exhaustedDigest: exhaustionDigest(&base, len(source))}
	covers := func(messages []provider.Message) bool {
		replay := replayReductionLedger(messages, nil)
		return state.coversTranscript(&replay)
	}
	if !covers(source) {
		t.Fatal("same transcript does not cover the exhausted prefix")
	}
	appended := append(append([]provider.Message(nil), source...), provider.Message{Role: provider.MessageRoleUser, Content: "three"})
	if !covers(appended) {
		t.Fatal("true append does not cover the exhausted prefix")
	}
	if covers(source[:1]) {
		t.Fatal("shrunk transcript covered the exhausted prefix")
	}
	edited := append([]provider.Message(nil), source...)
	edited[0].Content = "two"
	if len(edited[0].Content) != len(source[0].Content) {
		t.Fatal("test setup: edit should preserve length")
	}
	if covers(edited) {
		t.Fatal("same-length interior edit covered the exhausted prefix")
	}
	empty := reductionLedgerState{exhausted: true}
	nonEmpty := replayReductionLedger(source, nil)
	if empty.coversTranscript(&nonEmpty) {
		t.Fatal("empty exhausted prefix covered a non-empty transcript")
	}
	blank := replayReductionLedger(nil, nil)
	if !empty.coversTranscript(&blank) {
		t.Fatal("empty exhausted prefix did not cover an empty transcript")
	}
}
