package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func newTestDiagnosticsWriter(t *testing.T, streams diagnostics.Streams) (*diagnostics.Writer, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := diagnostics.New(diagnostics.Options{Dir: dir, Streams: streams, RunID: "run-test"})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, dir
}

func readCacheRecords(t *testing.T, dir string) []diagnostics.Record {
	t.Helper()
	path := filepath.Join(dir, "cache.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", path, err)
	}
	var out []diagnostics.Record
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec diagnostics.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestEmitCacheDiagnostic_NoOpWhenStreamDisabled(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: false})
	req := RunRequest{Diagnostics: w}
	emitCacheDiagnostic(req, &provider.UsageStats{PromptTokens: 10}, 1, "abcd1234", 0)

	if got := readCacheRecords(t, dir); len(got) != 0 {
		t.Fatalf("records = %d, want 0 when the cache stream is disabled", len(got))
	}
}

func TestEmitCacheDiagnostic_NoOpWhenUsageNil(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	req := RunRequest{Diagnostics: w}
	emitCacheDiagnostic(req, nil, 1, "abcd1234", 0)

	if got := readCacheRecords(t, dir); len(got) != 0 {
		t.Fatalf("records = %d, want 0 for a nil-usage response", len(got))
	}
}

func TestEmitCacheDiagnostic_NotGatedByUsageRecorder(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	req := RunRequest{Diagnostics: w, UsageRecorder: nil}
	emitCacheDiagnostic(req, &provider.UsageStats{PromptTokens: 10}, 1, "abcd1234", 0)

	if got := readCacheRecords(t, dir); len(got) != 1 {
		t.Fatalf("records = %d, want 1 even with no UsageRecorder configured", len(got))
	}
}

func TestEmitCacheDiagnostic_FieldsAndColdStart(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	req := RunRequest{
		Diagnostics: w,
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "my-anthropic",
			BackendModelID:        "claude-x",
			EffectiveProviderType: "anthropic",
		},
		PromptCacheKey: "super-secret-key",
		AgentID:        "agent-1",
		AgentType:      "explore",
	}
	usage := &provider.UsageStats{
		PromptTokens:             100,
		CompletionTokens:         20,
		CacheReadInputTokens:     40,
		CacheCreationInputTokens: 5,
	}
	emitCacheDiagnostic(req, usage, 3, "prefixhash", 2)
	emitCacheDiagnostic(req, usage, 4, "prefixhash2", 2)

	records := readCacheRecords(t, dir)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}

	first := records[0]
	if first.AgentID != "agent-1" || first.AgentType != "explore" || first.Turn != 3 {
		t.Fatalf("envelope fields = %+v, want agent-1/explore/turn 3", first)
	}

	raw, err := json.Marshal(first.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-key") {
		t.Fatalf("payload leaked the prompt cache key: %s", raw)
	}

	var payload cachePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ProviderAlias != "my-anthropic" || payload.BackendModelID != "claude-x" || payload.ProviderType != "anthropic" {
		t.Fatalf("model identity = %+v, want my-anthropic/claude-x/anthropic", payload)
	}
	if payload.PromptTokens != 100 || payload.CompletionTokens != 20 || payload.CacheReadTokens != 40 || payload.CacheCreateTokens != 5 {
		t.Fatalf("token counts = %+v, want 100/20/40/5", payload)
	}
	if payload.CacheKeyHash == "" || payload.CacheKeyHash == "super-secret-key" {
		t.Fatalf("CacheKeyHash = %q, want a non-empty hash that is not the key", payload.CacheKeyHash)
	}
	if !payload.ColdStart {
		t.Error("first record ColdStart = false, want true")
	}

	var second cachePayload
	if err := json.Unmarshal(mustMarshal(t, records[1].Payload), &second); err != nil {
		t.Fatalf("unmarshal second payload: %v", err)
	}
	if second.ColdStart {
		t.Error("second record ColdStart = true, want false (only one cold start per run)")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func resetColdStart(t *testing.T) {
	t.Helper()
	coldStartRecorded.Store(false)
	t.Cleanup(func() { coldStartRecorded.Store(false) })
}

func TestShortHash_EmptyStringYieldsEmptyHash(t *testing.T) {
	if got := shortHash(""); got != "" {
		t.Errorf("shortHash(\"\") = %q, want empty", got)
	}
	if got := shortHash("x"); len(got) != 8 {
		t.Errorf("shortHash(%q) length = %d, want 8", "x", len(got))
	}
}

func TestPerMessageHashes_DistinctContentDiffers(t *testing.T) {
	hashes := perMessageHashes([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi there"},
	})
	if len(hashes) != 2 {
		t.Fatalf("len(hashes) = %d, want 2", len(hashes))
	}
	if hashes[0] == hashes[1] {
		t.Errorf("hashes should differ for distinct messages, got %v", hashes)
	}
}

func TestLongestCommonPrefixLen_AppendOnlyKeepsFullPrefix(t *testing.T) {
	prev := perMessageHashes([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "one"},
		{Role: provider.MessageRoleAssistant, Content: "two"},
	})
	cur := perMessageHashes([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "one"},
		{Role: provider.MessageRoleAssistant, Content: "two"},
		{Role: provider.MessageRoleUser, Content: "three"},
	})
	if got := longestCommonPrefixLen(prev, cur); got != len(prev) {
		t.Errorf("longestCommonPrefixLen() = %d, want %d (pure append preserves the whole prior prefix)", got, len(prev))
	}
}

func TestLongestCommonPrefixLen_RewriteBreaksAtDivergence(t *testing.T) {
	prev := perMessageHashes([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "one"},
		{Role: provider.MessageRoleAssistant, Content: "two"},
		{Role: provider.MessageRoleUser, Content: "three"},
	})
	cur := perMessageHashes([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "one"},
		{Role: provider.MessageRoleAssistant, Content: "REWRITTEN"},
		{Role: provider.MessageRoleUser, Content: "three"},
	})
	if got := longestCommonPrefixLen(prev, cur); got != 1 {
		t.Errorf("longestCommonPrefixLen() = %d, want 1 (divergence at index 1)", got)
	}
}

func TestCumulativePrefixHash_EmptyMessagesYieldsEmptyHash(t *testing.T) {
	if got := cumulativePrefixHash(nil); got != "" {
		t.Errorf("cumulativePrefixHash(nil) = %q, want empty", got)
	}
}

// TestTurnProgressor_PromotesPendingHashesOnlyWhenModelCallIssued exercises
// prepareTurn plus the promotion in executeModelCall through a real Runner
// loop, verifying shared_prefix_messages reflects the last request actually
// sent rather than every prepareTurn attempt.
func TestTurnProgressor_PromotesPendingHashesOnlyWhenModelCallIssued(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})

	calls := 0
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			calls++
			return provider.ChatResponse{
				Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"},
				Usage:   &provider.UsageStats{PromptTokens: 10, CompletionTokens: 1},
			}, nil
		},
	}

	req := RunRequest{
		Provider:      providerStub,
		Executor:      noopExecutor{},
		Diagnostics:   w,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 1},
		Events: output.NoopSink{},
	}

	runner := NewRunner()
	if _, err := runner.Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	records := readCacheRecords(t, dir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	var payload cachePayload
	if err := json.Unmarshal(mustMarshal(t, records[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.PrefixHash == "" {
		t.Error("PrefixHash is empty, want a non-empty hash for a non-empty conversation")
	}
	if payload.SharedPrefixMessages != 0 {
		t.Errorf("SharedPrefixMessages = %d, want 0 for the first request of the run", payload.SharedPrefixMessages)
	}
}

type noopExecutor struct{}

func (noopExecutor) Execute(context.Context, string, string, map[string]any) (any, error) {
	return nil, nil
}
