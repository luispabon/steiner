package provider

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// claudeSubRunMeasuredTurn runs one request through the provider's stream and
// returns its terminal chunk, which claudeSubStream stamps with the call's
// measurements.
func claudeSubRunMeasuredTurn(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) ChatChunk {
	t.Helper()
	stream, err := NewClaudeSubscriptionProvider(pool, nil).StreamChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	var final ChatChunk
	for chunk := range stream {
		if chunk.Done {
			final = chunk
		}
	}
	if !final.Done {
		t.Fatal("turn emitted no Done chunk")
	}
	if final.Error != "" {
		t.Fatalf("turn failed: %s", final.Error)
	}
	if final.Diagnostics == nil || final.Diagnostics.ClaudeSub == nil {
		t.Fatalf("terminal chunk diagnostics = %+v, want claude_subscription measurements", final.Diagnostics)
	}
	return final
}

// TestClaudeSubCallDiagnosticsProcessDisposition proves a live process reports
// reuse and a restarted session reports resume.
func TestClaudeSubCallDiagnosticsProcessDisposition(t *testing.T) {
	model := claudeSubTestSpec().Model
	t.Run("first call spawns then an append reuses", func(t *testing.T) {
		pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2))
		first := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes")},
		})
		if got := first.Diagnostics.ClaudeSub.Process; got != claudeSubProcessSpawn {
			t.Fatalf("first call process = %q, want %q", got, claudeSubProcessSpawn)
		}
		if got := first.Diagnostics.ClaudeSub.ReseedReason; got != "" {
			t.Fatalf("first call reseed reason = %q, want empty", got)
		}
		second := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")},
			BackendState:     first.BackendState,
		})
		if got := second.Diagnostics.ClaudeSub.Process; got != claudeSubProcessReuse {
			t.Fatalf("second call process = %q, want %q", got, claudeSubProcessReuse)
		}
		if n := spawner.callCount(); n != 1 {
			t.Fatalf("spawn count = %d, want 1 (the live process took the append)", n)
		}
	})
	t.Run("restart resumes the stored session", func(t *testing.T) {
		assistant, st := claudeSubOpeningTurn(t)
		pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
		final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
			BackendState:     claudeSubEncodeState(t, st),
		})
		if got := final.Diagnostics.ClaudeSub.Process; got != claudeSubProcessResume {
			t.Fatalf("process = %q, want %q", got, claudeSubProcessResume)
		}
		if got := final.Diagnostics.ClaudeSub.ReseedReason; got != "" {
			t.Fatalf("resume reseed reason = %q, want empty", got)
		}
	})
}

// TestClaudeSubCallDiagnosticsRestoreReseedReason proves a restarted call that
// cannot resume reports the restore branch that made it reseed.
func TestClaudeSubCallDiagnosticsRestoreReseedReason(t *testing.T) {
	tests := []struct {
		name    string
		noState bool
		mutate  func(*claudeSubBackendState)
		want    string
	}{
		{name: "no stored state", noState: true, want: claudeSubReseedNoState},
		{name: "stale fingerprint", mutate: func(st *claudeSubBackendState) { st.PromptToolsFingerprint = "stale" }, want: claudeSubReseedFingerprint},
		{name: "missing final uuid", mutate: func(st *claudeSubBackendState) { st.FinalUUID = "" }, want: claudeSubReseedFinalUUID},
		{name: "stored prefix mismatch", mutate: func(st *claudeSubBackendState) { st.SyncPrefixDigest = "bad" }, want: claudeSubReseedPrefix},
		{name: "foreign schema version", mutate: func(st *claudeSubBackendState) { st.Version = claudeSubBackendStateVersion + 1 }, want: claudeSubReseedNoState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assistant, st := claudeSubOpeningTurn(t)
			var raw json.RawMessage
			if !tt.noState {
				if tt.mutate != nil {
					tt.mutate(&st)
				}
				raw = claudeSubEncodeState(t, st)
			}
			pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            claudeSubTestSpec().Model,
				Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
				BackendState:     raw,
			})
			if got := final.Diagnostics.ClaudeSub.Process; got != claudeSubProcessSpawn {
				t.Errorf("process = %q, want %q", got, claudeSubProcessSpawn)
			}
			if got := final.Diagnostics.ClaudeSub.ReseedReason; got != tt.want {
				t.Errorf("reseed reason = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestClaudeSubCallDiagnosticsLiveReseedReason proves a live process that cannot
// take the request reports the branch that retired it.
func TestClaudeSubCallDiagnosticsLiveReseedReason(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*claudeSubBackendState)
		rewrite bool
		dead    bool
		want    string
	}{
		{name: "checkpoint mismatch", mutate: func(st *claudeSubBackendState) { st.SyncChainDigest = "bad" }, want: claudeSubReseedCheckpoint},
		{name: "rewritten history", rewrite: true, want: claudeSubReseedPlan},
		{name: "exited process", dead: true, want: claudeSubReseedExited},
		// The state belongs to another transport session and no longer
		// reproduces the request prefix, so the fork cannot resume and the call
		// reseeds on a fresh process.
		{name: "owner fork without a restorable state", mutate: func(st *claudeSubBackendState) {
			st.TransportSession = "previous-owner"
			st.SyncPrefixDigest = "bad"
		}, want: claudeSubReseedOwnerFork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := claudeSubTestSpec().Model
			pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
			first := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				Messages:         []Message{claudeSubUserText("read notes")},
			})
			st := claudeSubDecodeState(t, first.BackendState)
			if tt.mutate != nil {
				tt.mutate(&st)
			}
			if tt.dead {
				if err := spawner.call(t, 0).Conn.Close(context.Background()); err != nil {
					t.Fatalf("close fake CLI: %v", err)
				}
				claudeSubWaitExited(t, pool, "parent")
			}
			msgs := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
			if tt.rewrite {
				msgs[0] = claudeSubUserText("rewritten")
			}
			final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				Messages:         msgs,
				BackendState:     claudeSubEncodeState(t, st),
			})
			if got := final.Diagnostics.ClaudeSub.Process; got != claudeSubProcessSpawn {
				t.Errorf("process = %q, want %q", got, claudeSubProcessSpawn)
			}
			if got := final.Diagnostics.ClaudeSub.ReseedReason; got != tt.want {
				t.Errorf("reseed reason = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestClaudeSubCallDiagnosticsUsageGate proves the call records the gate's poll
// count and elapsed time.
func TestClaudeSubCallDiagnosticsUsageGate(t *testing.T) {
	claudeSubUsageGateFast(t, 5*time.Second, time.Millisecond)
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "gate", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn
	polls := 0
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
		var envelope struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &envelope) != nil {
			return nil
		}
		switch envelope.Type {
		case "control_request":
			if envelope.Request.Subtype != "get_usage" {
				return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, nil)}
			}
			polls++
			if polls == 1 {
				return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":false}`))}
			}
			return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
		case "user":
			for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl") {
				conn.push(ev)
			}
		}
		return nil
	}
	conn.mu.Unlock()

	final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession: "gate",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("hello")},
	})
	diagnostics := final.Diagnostics.ClaudeSub
	if got := diagnostics.UsageGatePolls; got != 2 {
		t.Fatalf("UsageGatePolls = %d, want 2 (one not-ready poll, one ready poll)", got)
	}
	if got := diagnostics.UsageGatePolls; got != polls {
		t.Fatalf("UsageGatePolls = %d, want the %d the process answered", got, polls)
	}
	if diagnostics.UsageGateMillis < 1 {
		t.Fatalf("UsageGateMillis = %d, want at least the 1ms repoll delay", diagnostics.UsageGateMillis)
	}
	if diagnostics.UsageProbeSpawned {
		t.Error("UsageProbeSpawned = true, want false when the live process confirms")
	}
}

// TestClaudeSubCallMetricsResetDelivery proves a second delivery attempt keeps
// the gate cost a failed attempt already spent and the probe it may have
// spawned, while dropping the failed attempt's own disposition.
func TestClaudeSubCallMetricsResetDelivery(t *testing.T) {
	metrics := &claudeSubCallMetrics{
		process:           claudeSubProcessResume,
		reseedReason:      claudeSubReseedCheckpoint,
		usageGate:         42 * time.Millisecond,
		usageGatePolls:    3,
		usageProbeSpawned: true,
	}
	metrics.resetDelivery()
	if metrics.process != "" {
		t.Errorf("process = %q, want cleared", metrics.process)
	}
	if metrics.reseedReason != "" {
		t.Errorf("reseed reason = %q, want cleared: it describes the failed attempt", metrics.reseedReason)
	}
	if metrics.usageGate != 42*time.Millisecond || metrics.usageGatePolls != 3 {
		t.Errorf("usage gate = %v/%d polls, want the failed attempt's cost kept for the call", metrics.usageGate, metrics.usageGatePolls)
	}
	if !metrics.usageProbeSpawned {
		t.Error("usageProbeSpawned = false, want sticky for the call after a failed attempt spawned one")
	}
}

// TestClaudeSubCallMetricsGateAccumulates proves the usage gate's time and poll
// count add up across the delivery attempts of one call.
func TestClaudeSubCallMetricsGateAccumulates(t *testing.T) {
	metrics := &claudeSubCallMetrics{process: claudeSubProcessResume}
	metrics.noteUsageGate(20*time.Millisecond, 2)
	metrics.resetDelivery()
	metrics.noteReseed(claudeSubReseedRetry)
	metrics.noteUsageGate(30*time.Millisecond, 1)
	if got := metrics.usageGate; got != 50*time.Millisecond {
		t.Errorf("usage gate = %v, want 50ms across both attempts", got)
	}
	if got := metrics.usageGatePolls; got != 3 {
		t.Errorf("usage gate polls = %d, want 3 across both attempts", got)
	}
	if got := metrics.process; got != "" {
		t.Errorf("process = %q, want cleared until the retry reports its own", got)
	}
	if got := metrics.reseedReason; got != claudeSubReseedRetry {
		t.Errorf("reseed reason = %q, want %q", got, claudeSubReseedRetry)
	}
	diagnostics := metrics.diagnostics()
	if diagnostics == nil || diagnostics.ClaudeSub == nil {
		t.Fatalf("diagnostics = %+v, want claude_subscription measurements", diagnostics)
	}
	if got := diagnostics.ClaudeSub.UsageGateMillis; got != 50 {
		t.Errorf("UsageGateMillis = %d, want 50", got)
	}
}

// TestClaudeSubCallDiagnosticsSummaryForkFallback proves a summary whose fork
// helper fails before output reports the live parent that actually answered, not
// the failed helper.
func TestClaudeSubCallDiagnosticsSummaryForkFallback(t *testing.T) {
	helper := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubFailedResumeBatch()}}
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 3), helper)
	_, a1, _, _, second := claudeSubTwoTurnParent(t, pool)
	final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{claudeSubUserText("first question"), a1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and the failed fork helper)", got)
	}
	if _, ok := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); !ok {
		t.Fatal("fork helper did not resume: the fallback was not exercised")
	}
	if got := final.Diagnostics.ClaudeSub.Process; got != claudeSubProcessReuse {
		t.Errorf("process = %q, want %q (the live parent answered in place)", got, claudeSubProcessReuse)
	}
}

// TestClaudeSubCallDiagnosticsSummaryColdReseed proves a summary with no live
// parent reports the helper reseed and a spawned process.
func TestClaudeSubCallDiagnosticsSummaryColdReseed(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	msgs := []Message{claudeSubUserText("first question"), {Role: MessageRoleAssistant, Content: "first answer"}, claudeSubUserText("summarize")}
	final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 msgs,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawned %d processes, want 1 cold helper", got)
	}
	if got := final.Diagnostics.ClaudeSub.Process; got != claudeSubProcessSpawn {
		t.Errorf("process = %q, want %q", got, claudeSubProcessSpawn)
	}
	if got := final.Diagnostics.ClaudeSub.ReseedReason; got != claudeSubReseedHelper {
		t.Errorf("reseed reason = %q, want %q", got, claudeSubReseedHelper)
	}
}

// TestClaudeSubCallDiagnosticsUsageProbe proves the call records the fresh probe
// process that confirms a live process's unconfirmable extra-usage state, and
// that a refusing probe keeps the call fail-closed with the probe still
// recorded.
func TestClaudeSubCallDiagnosticsUsageProbe(t *testing.T) {
	notReady := json.RawMessage(`{"rate_limits_available":false}`)
	t.Run("fresh probe confirms", func(t *testing.T) {
		// The gate spends its whole poll budget on the first poll, so only the
		// fresh process can confirm the account.
		claudeSubUsageGateFast(t, 0)
		live := &claudeSubScript{
			batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")},
			usage:   notReady,
		}
		pool, spawner, _ := claudeSubUsageProbeTurnPool(t, "probe-confirms", live, claudeSubFixture(t, "get_usage_ok.json"))
		final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "probe-confirms",
			Model:            claudeSubTestSpec().Model,
			Messages:         []Message{claudeSubUserText("hello")},
		})
		if n := spawner.callCount(); n != 2 {
			t.Fatalf("spawn count = %d, want 2 (live process and fresh probe)", n)
		}
		diagnostics := final.Diagnostics.ClaudeSub
		if !diagnostics.UsageProbeSpawned {
			t.Error("UsageProbeSpawned = false, want true after the fresh probe confirmed the account")
		}
		if got := diagnostics.UsageGatePolls; got != 1 {
			t.Errorf("UsageGatePolls = %d, want 1 (only the live process was polled)", got)
		}
	})
	t.Run("fresh probe refuses", func(t *testing.T) {
		claudeSubUsageGateFast(t, 0)
		live := &claudeSubScript{
			batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")},
			usage:   notReady,
		}
		pool, spawner, _ := claudeSubUsageProbeTurnPool(t, "probe-refuses", live, notReady)
		metrics := &claudeSubCallMetrics{}
		err := claudeSubTurn(withClaudeSubMetrics(context.Background(), metrics), pool, ChatRequest{
			TransportSession: "probe-refuses",
			Model:            claudeSubTestSpec().Model,
			Messages:         []Message{claudeSubUserText("hello")},
		}, func(ChatChunk) error { return nil })
		if !errors.Is(err, errClaudeSubUsageNotReady) {
			t.Fatalf("turn error = %v, want the fail-closed usage refusal", err)
		}
		if n := spawner.callCount(); n != 2 {
			t.Errorf("spawn count = %d, want 2 (live process and refusing probe)", n)
		}
		diagnostics := metrics.diagnostics()
		if diagnostics == nil || diagnostics.ClaudeSub == nil {
			t.Fatalf("diagnostics = %+v, want claude_subscription measurements", diagnostics)
		}
		if !diagnostics.ClaudeSub.UsageProbeSpawned {
			t.Error("UsageProbeSpawned = false, want true: the probe process started even though it refused")
		}
		if got := diagnostics.ClaudeSub.UsageGatePolls; got != 1 {
			t.Errorf("UsageGatePolls = %d, want 1", got)
		}
	})
}

// TestClaudeSubChatCompletionReturnsCallDiagnostics proves the folded response
// carries the measurements the terminal chunk reported.
func TestClaudeSubChatCompletionReturnsCallDiagnostics(t *testing.T) {
	pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	response, err := NewClaudeSubscriptionProvider(pool, nil).ChatCompletion(context.Background(), ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("hello")},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if response.Diagnostics == nil || response.Diagnostics.ClaudeSub == nil {
		t.Fatalf("diagnostics = %+v, want claude_subscription measurements", response.Diagnostics)
	}
	if got := response.Diagnostics.ClaudeSub.Process; got != claudeSubProcessSpawn {
		t.Errorf("process = %q, want %q", got, claudeSubProcessSpawn)
	}
	if got := response.Diagnostics.ClaudeSub.ReseedReason; got != "" {
		t.Errorf("reseed reason = %q, want empty for a fresh call", got)
	}
}

// TestClaudeSubCallDiagnosticsRetryReseed proves a resumed process that fails
// before output reports the retry reseed, and the retry's own process, on the
// terminal chunk.
func TestClaudeSubCallDiagnosticsRetryReseed(t *testing.T) {
	errEvent := claudeSubEvent{
		Type:      "result",
		Subtype:   "error_during_execution",
		SessionID: "sess-1",
		Raw:       json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"No conversation found with session ID: sess-1","session_id":"sess-1"}`),
	}
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{{errEvent}}}, claudeSubTextTurns(t, 1))
	final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (failed resume, then reseed)", n)
	}
	if got := final.Diagnostics.ClaudeSub.Process; got != claudeSubProcessSpawn {
		t.Errorf("process = %q, want %q: the failed resume attempt's disposition must be dropped", got, claudeSubProcessSpawn)
	}
	if got := final.Diagnostics.ClaudeSub.ReseedReason; got != claudeSubReseedRetry {
		t.Errorf("reseed reason = %q, want %q", got, claudeSubReseedRetry)
	}
}

// claudeSubMeasuredTurnAsync runs one measured turn and returns its terminal
// chunk. It uses no testing helpers, so it is safe on a goroutine.
func claudeSubMeasuredTurnAsync(pool *ClaudeSubscriptionPool, req ChatRequest) (ChatChunk, error) {
	stream, err := NewClaudeSubscriptionProvider(pool, nil).StreamChatCompletion(context.Background(), req)
	if err != nil {
		return ChatChunk{}, err
	}
	var final ChatChunk
	for chunk := range stream {
		if chunk.Done {
			final = chunk
		}
	}
	if !final.Done {
		return ChatChunk{}, errors.New("turn emitted no Done chunk")
	}
	if final.Error != "" {
		return ChatChunk{}, errors.New(final.Error)
	}
	if final.Diagnostics == nil || final.Diagnostics.ClaudeSub == nil {
		return ChatChunk{}, errors.New("terminal chunk carries no claude_subscription measurements")
	}
	return final, nil
}

// TestClaudeSubCallDiagnosticsConcurrentCalls proves concurrent calls keep their
// own measurements: the resumed call reports resume, the cold call reports
// spawn, and neither sees the other's reseed.
func TestClaudeSubCallDiagnosticsConcurrentCalls(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	scripts := []*claudeSubScript{claudeSubTextTurns(t, 1), claudeSubTextTurns(t, 1)}
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	next := 0
	spawner.onSpawn = func(c *claudeSubFakeConn) {
		if next >= len(scripts) {
			return
		}
		sc := scripts[next]
		next++
		c.mu.Lock()
		c.responder = sc.respond
		c.mu.Unlock()
	}
	calls := []struct {
		name string
		req  ChatRequest
	}{
		{name: claudeSubProcessResume, req: ChatRequest{
			TransportSession: "concurrent-resume",
			Model:            claudeSubTestSpec().Model,
			Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
			BackendState:     claudeSubEncodeState(t, st),
		}},
		{name: claudeSubProcessSpawn, req: ChatRequest{
			TransportSession: "concurrent-spawn",
			Model:            claudeSubTestSpec().Model,
			Messages:         []Message{claudeSubUserText("read notes")},
		}},
	}
	type outcome struct {
		name  string
		final ChatChunk
		err   error
	}
	outcomes := make(chan outcome, len(calls))
	var wg sync.WaitGroup
	for _, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			final, err := claudeSubMeasuredTurnAsync(pool, call.req)
			outcomes <- outcome{name: call.name, final: final, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)
	want := map[string]string{claudeSubProcessResume: claudeSubProcessResume, claudeSubProcessSpawn: claudeSubProcessSpawn}
	for got := range outcomes {
		if got.err != nil {
			t.Errorf("%s call: %v", got.name, got.err)
			continue
		}
		if process := got.final.Diagnostics.ClaudeSub.Process; process != want[got.name] {
			t.Errorf("%s call process = %q, want %q", got.name, process, want[got.name])
		}
		if reason := got.final.Diagnostics.ClaudeSub.ReseedReason; reason != "" {
			t.Errorf("%s call reseed reason = %q, want empty", got.name, reason)
		}
	}
	if n := spawner.callCount(); n != 2 {
		t.Errorf("spawn count = %d, want 2 (one process per call)", n)
	}
}
