package provider

import (
	"context"
	"encoding/json"
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

// TestClaudeSubCallMetricsResetDelivery proves a second delivery attempt reports
// its own process and gate cost while a probe the failed attempt spawned stays
// recorded for the call.
func TestClaudeSubCallMetricsResetDelivery(t *testing.T) {
	metrics := &claudeSubCallMetrics{
		process:           claudeSubProcessResume,
		usageGate:         42 * time.Millisecond,
		usageGatePolls:    3,
		usageProbeSpawned: true,
	}
	metrics.resetDelivery()
	if metrics.process != "" {
		t.Errorf("process = %q, want cleared", metrics.process)
	}
	if metrics.usageGate != 0 || metrics.usageGatePolls != 0 {
		t.Errorf("usage gate = %v/%d polls, want cleared", metrics.usageGate, metrics.usageGatePolls)
	}
	if !metrics.usageProbeSpawned {
		t.Error("usageProbeSpawned = false, want sticky for the call after a failed attempt spawned one")
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
