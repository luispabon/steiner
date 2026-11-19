package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

type wireFingerprintCall struct {
	stream    bool
	cacheable int
	shared    int
	model     string
	messages  int
}

// fingerprintingProvider is a fakeProvider that also implements
// provider.CacheFingerprinter, so the agent's send-time hook has a capability
// to call without building a real wire.
type fingerprintingProvider struct {
	*fakeProvider
	fingerprint provider.WireCacheDiagnostics
	err         error
	calls       []wireFingerprintCall
}

func (p *fingerprintingProvider) CacheFingerprint(_ context.Context, req provider.ChatRequest, stream bool, cacheable, shared int) (provider.WireCacheDiagnostics, error) {
	p.calls = append(p.calls, wireFingerprintCall{
		stream:    stream,
		cacheable: cacheable,
		shared:    shared,
		model:     req.Model,
		messages:  len(req.Messages),
	})
	if p.err != nil {
		return provider.WireCacheDiagnostics{}, p.err
	}
	fingerprint := p.fingerprint
	fingerprint.Stream = stream
	return fingerprint, nil
}

func streamingUsageProvider(usage *provider.UsageStats) *fakeProvider {
	chunks := make(chan provider.ChatChunk, 1)
	chunks <- provider.ChatChunk{Done: true, FinishReason: "stop", Usage: usage}
	close(chunks)
	return &fakeProvider{streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
		return chunks, nil
	}}
}

func decodeCachePayload(t *testing.T, record diagnostics.Record) cachePayload {
	t.Helper()
	var payload cachePayload
	if err := json.Unmarshal(mustMarshal(t, record.Payload), &payload); err != nil {
		t.Fatalf("unmarshal cache payload: %v", err)
	}
	return payload
}

func TestCompleteModelCallCapturesWireFingerprintAtSend(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	prov := &fingerprintingProvider{
		fakeProvider: streamingUsageProvider(&provider.UsageStats{PromptTokens: 10, CompletionTokens: 1}),
		fingerprint: provider.WireCacheDiagnostics{
			CacheablePrefixHash: "cacheable-hash",
			SharedPrefixHash:    "shared-hash",
			BodyKeyHash:         "body-key-hash",
			SessionIDHash:       "session-hash",
			ThreadIDHash:        "thread-hash",
			OriginatorHash:      "originator-hash",
			AffinityKeysMatch:   true,
			HasTools:            true,
			ToolCount:           2,
			HasReasoning:        true,
			Store:               true,
		},
	}
	req := RunRequest{
		Provider:           prov,
		Diagnostics:        w,
		CacheBaseline:      NewCacheBaselineStore(),
		PromptCacheKey:     "super-secret-key",
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "m", EffectiveProviderType: "codex"},
		Events:             output.NoopSink{},
		StreamingPreferred: true,
	}
	chatRequest := provider.ChatRequest{
		Model:          "m",
		PromptCacheKey: "super-secret-key",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "stable system"},
			{Role: provider.MessageRoleUser, Content: "first prompt"},
			{Role: provider.MessageRoleUser, Content: "newest prompt"},
		},
	}
	if _, _, err := completeModelCall(context.Background(), req, 1, chatRequest, nil, prompt.ModelTokenBudget{}, nil); err != nil {
		t.Fatalf("completeModelCall() error = %v", err)
	}

	if len(prov.calls) != 1 {
		t.Fatalf("fingerprint calls = %d, want 1", len(prov.calls))
	}
	call := prov.calls[0]
	if !call.stream {
		t.Error("fingerprint stream = false, want true for a streaming send")
	}
	if call.cacheable != 2 || call.shared != 0 || call.messages != 3 {
		t.Errorf("fingerprint call = %+v, want cacheable 2, shared 0, 3 messages", call)
	}
	if call.model != "m" {
		t.Errorf("fingerprinted model = %q, want the request actually sent", call.model)
	}

	records := readCacheRecords(t, dir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	payload := decodeCachePayload(t, records[0])
	if payload.WireCache == nil {
		t.Fatal("wire block is absent, want the fingerprinted payload")
	}
	wire := payload.WireCache
	if wire.CacheablePrefixHash != "cacheable-hash" || wire.SharedPrefixHash != "shared-hash" {
		t.Errorf("wire hashes = %q/%q, want cacheable-hash/shared-hash", wire.CacheablePrefixHash, wire.SharedPrefixHash)
	}
	if wire.BodyKeyHash != "body-key-hash" || wire.SessionIDHash != "session-hash" || wire.ThreadIDHash != "thread-hash" || wire.OriginatorHash != "originator-hash" {
		t.Errorf("wire header hashes = %+v, want the fingerprinter values", wire)
	}
	if !wire.Stream || !wire.HasTools || wire.ToolCount != 2 || !wire.HasReasoning || !wire.Store || !wire.AffinityKeysMatch {
		t.Errorf("wire flags = %+v, want streamed call with tools, reasoning, and store", wire)
	}

	raw := string(mustMarshal(t, records[0].Payload))
	for _, secret := range []string{"super-secret-key", "stable system", "first prompt", "newest prompt"} {
		if strings.Contains(raw, secret) {
			t.Errorf("payload leaked %q: %s", secret, raw)
		}
	}
}

func TestCompleteModelCallSkipsWireFingerprintWhenCacheStreamDisabled(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{})
	prov := &fingerprintingProvider{fakeProvider: streamingUsageProvider(&provider.UsageStats{PromptTokens: 10})}
	req := RunRequest{
		Provider:           prov,
		Diagnostics:        w,
		CacheBaseline:      NewCacheBaselineStore(),
		PromptCacheKey:     "key",
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "m"},
		Events:             output.NoopSink{},
		StreamingPreferred: true,
	}
	if _, _, err := completeModelCall(context.Background(), req, 1, provider.ChatRequest{Model: "m", Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}}}, nil, prompt.ModelTokenBudget{}, nil); err != nil {
		t.Fatalf("completeModelCall() error = %v", err)
	}
	if len(prov.calls) != 0 {
		t.Fatalf("fingerprint calls = %d, want 0 while the cache stream is disabled", len(prov.calls))
	}
	if got := readCacheRecords(t, dir); len(got) != 0 {
		t.Fatalf("records = %d, want 0", len(got))
	}
}

func TestExecuteChatRequestObservesEachDeliveryAttempt(t *testing.T) {
	prov := &fakeProvider{
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			return nil, errors.New("stream unavailable")
		},
		chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"}, Usage: &provider.UsageStats{PromptTokens: 1}}, nil
		},
	}
	var modes []bool
	var models []string
	onSend := func(req provider.ChatRequest, stream bool) {
		modes = append(modes, stream)
		models = append(models, req.Model)
	}
	_, _, err := executeChatRequestWithOptions(context.Background(), prov, 1, provider.ChatRequest{Model: "chat-model"}, prompt.ModelTokenBudget{}, output.NoopSink{}, nil, false, true, nil, chatExecOptions{onSend: onSend}, nil)
	if err != nil {
		t.Fatalf("executeChatRequestWithOptions() error = %v", err)
	}
	if len(modes) != 2 || modes[0] != true || modes[1] != false {
		t.Fatalf("observed modes = %v, want [true false] for stream then fallback chat", modes)
	}
	if len(models) != 2 || models[0] != "chat-model" || models[1] != "chat-model" {
		t.Fatalf("observed models = %v, want the request actually sent", models)
	}
}

func TestCompleteCompactionCallCapturesWireFingerprint(t *testing.T) {
	resetColdStart(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	prov := &fingerprintingProvider{
		fakeProvider: streamingUsageProvider(&provider.UsageStats{PromptTokens: 20, CompletionTokens: 4}),
		fingerprint:  provider.WireCacheDiagnostics{CacheablePrefixHash: "compaction-cacheable"},
	}
	req := RunRequest{
		Provider:       prov,
		Diagnostics:    w,
		CacheBaseline:  NewCacheBaselineStore(),
		PromptCacheKey: "key",
		ResolvedModel:  provider.ResolvedModel{BackendModelID: "model"},
		Events:         output.NoopSink{},
	}
	initial := []provider.Message{{Role: provider.MessageRoleUser, Content: "original"}}
	promoteRequestCacheStats(req, computeRequestCacheStats(req, initial))

	compactionMessages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "original"},
		{Role: provider.MessageRoleUser, Content: "compaction instruction"},
	}
	if _, err := completeCompactionCall(context.Background(), req, 2, provider.ChatRequest{Model: "model", Messages: compactionMessages}, prompt.ModelTokenBudget{}, nil); err != nil {
		t.Fatalf("completeCompactionCall() error = %v", err)
	}

	if len(prov.calls) != 1 {
		t.Fatalf("fingerprint calls = %d, want 1", len(prov.calls))
	}
	if !prov.calls[0].stream {
		t.Error("fingerprint stream = false, want true for a compaction send")
	}
	if prov.calls[0].cacheable != 1 || prov.calls[0].shared != 1 {
		t.Errorf("fingerprint call = %+v, want cacheable 1 and shared 1", prov.calls[0])
	}

	records := readCacheRecords(t, dir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	payload := decodeCachePayload(t, records[0])
	if payload.CallKind != "compaction" {
		t.Fatalf("call kind = %q, want compaction", payload.CallKind)
	}
	if payload.WireCache == nil || payload.WireCache.CacheablePrefixHash != "compaction-cacheable" || !payload.WireCache.Stream {
		t.Fatalf("wire block = %+v, want the compaction fingerprint with stream true", payload.WireCache)
	}
}

func TestCaptureWireCacheFingerprintFingerprintErrorIsNotMeasured(t *testing.T) {
	prov := &fingerprintingProvider{
		fakeProvider: &fakeProvider{},
		err:          errors.New("payload unavailable"),
	}
	if _, measured := captureWireCacheFingerprint(context.Background(), prov, provider.ChatRequest{Model: "m"}, true, requestCacheStats{}); measured {
		t.Error("measured = true, want false when the fingerprinter fails")
	}
	if _, measured := captureWireCacheFingerprint(context.Background(), &fakeProvider{}, provider.ChatRequest{Model: "m"}, true, requestCacheStats{}); measured {
		t.Error("measured = true, want false for a provider without CacheFingerprinter")
	}
}

func TestEmitCacheDiagnosticResumeBaselineUnavailable(t *testing.T) {
	resetColdStart(t)
	resetResumeLatch(t)
	w, dir := newTestDiagnosticsWriter(t, diagnostics.Streams{Cache: true})
	req := RunRequest{Diagnostics: w}
	usage := &provider.UsageStats{PromptTokens: 10}
	noPredecessor := requestCacheStats{prefixHash: "aaaa", comparisonEnabled: true}
	withPredecessor := requestCacheStats{prefixHash: "bbbb", comparisonEnabled: true, predecessorKnown: true, sharedPrefixMessages: 1}

	NoteInteractiveResume()
	emitCacheDiagnostic(req, provider.ChatResponse{Usage: usage}, "", 1, noPredecessor)
	emitCacheDiagnostic(req, provider.ChatResponse{Usage: usage}, "", 2, noPredecessor)

	records := readCacheRecords(t, dir)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if first := string(mustMarshal(t, records[0].Payload)); !strings.Contains(first, `"resume_baseline_unavailable":true`) {
		t.Errorf("first payload = %s, want resume_baseline_unavailable true", first)
	}
	if second := string(mustMarshal(t, records[1].Payload)); strings.Contains(second, "resume_baseline_unavailable") {
		t.Errorf("second payload = %s, want the resume label only on the first record", second)
	}

	// A predecessor after a resume means the baseline was available, so the
	// latch is consumed without a label.
	NoteInteractiveResume()
	emitCacheDiagnostic(req, provider.ChatResponse{Usage: usage}, "", 3, withPredecessor)
	if got := readCacheRecords(t, dir); strings.Contains(string(mustMarshal(t, got[2].Payload)), "resume_baseline_unavailable") {
		t.Errorf("record with a predecessor carried the resume label: %s", mustMarshal(t, got[2].Payload))
	}

	// A comparison that never ran cannot claim the latch: the label needs a
	// real comparison, so it stays armed for the first record that has one.
	NoteInteractiveResume()
	emitCacheDiagnostic(req, provider.ChatResponse{Usage: usage}, "", 4, requestCacheStats{prefixHash: "cccc"})
	emitCacheDiagnostic(req, provider.ChatResponse{Usage: usage}, "", 5, noPredecessor)
	records = readCacheRecords(t, dir)
	if strings.Contains(string(mustMarshal(t, records[3].Payload)), "resume_baseline_unavailable") {
		t.Error("record with comparison disabled claimed the resume latch")
	}
	if !strings.Contains(string(mustMarshal(t, records[4].Payload)), `"resume_baseline_unavailable":true`) {
		t.Error("first compared record after the disabled comparison did not claim the resume latch")
	}

	// A delegated child has its own baseline identity and never consumes the
	// parent's resume label.
	NoteInteractiveResume()
	child := req
	child.AgentID = "child-1"
	emitCacheDiagnostic(child, provider.ChatResponse{Usage: usage}, "", 6, noPredecessor)
	emitCacheDiagnostic(req, provider.ChatResponse{Usage: usage}, "", 7, noPredecessor)
	records = readCacheRecords(t, dir)
	if strings.Contains(string(mustMarshal(t, records[5].Payload)), "resume_baseline_unavailable") {
		t.Error("delegated child record claimed the parent's resume label")
	}
	if !strings.Contains(string(mustMarshal(t, records[6].Payload)), `"resume_baseline_unavailable":true`) {
		t.Error("parent record after a child record did not claim the resume label")
	}
}

func resetResumeLatch(t *testing.T) {
	t.Helper()
	interactiveResumePending.Store(false)
	t.Cleanup(func() { interactiveResumePending.Store(false) })
}
