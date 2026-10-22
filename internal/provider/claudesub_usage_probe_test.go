package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// claudeSubUsageProbeFast shortens the fresh-process confirmation timeout for
// one test. Tests using it must not run in parallel.
func claudeSubUsageProbeFast(t *testing.T, timeout time.Duration) {
	t.Helper()
	prev := claudeSubUsageProbeTimeout
	claudeSubUsageProbeTimeout = timeout
	t.Cleanup(func() { claudeSubUsageProbeTimeout = prev })
}

// claudeSubUsageProbeReply scripts every process the pool starts after this call
// to answer get_usage with usages in order, the last entry repeating. It uses
// the spawner hook, so no global probe seam is needed. At least one usage is
// required.
func claudeSubUsageProbeReply(spawner *claudeSubTestSpawner, usages ...json.RawMessage) {
	// onSpawn runs under the spawner's mutex, so the index needs no extra lock.
	calls := 0
	spawner.onSpawn = func(conn *claudeSubFakeConn) {
		usage := usages[min(calls, len(usages)-1)]
		calls++
		conn.mu.Lock()
		conn.responder = claudeSubQuotaUsageResponder(usage)
		conn.mu.Unlock()
	}
}

// claudeSubUsageProbeKeys returns the pool's session keys under one mutex hold.
func claudeSubUsageProbeKeys(pool *ClaudeSubscriptionPool) []string {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	keys := make([]string, 0, len(pool.sessions))
	for key := range pool.sessions {
		keys = append(keys, key)
	}
	return keys
}

// claudeSubUsageProbeCount counts the lines conn sent that contain needle.
func claudeSubUsageProbeCount(conn *claudeSubFakeConn, needle string) int {
	n := 0
	for _, line := range conn.sentLines() {
		if strings.Contains(line, needle) {
			n++
		}
	}
	return n
}

// claudeSubUsageProbeTurnPool starts the live session under key from live and
// scripts every later process as a fresh usage confirmation answering probeUsage.
func claudeSubUsageProbeTurnPool(t *testing.T, key string, live *claudeSubScript, probeUsage json.RawMessage) (*ClaudeSubscriptionPool, *claudeSubTestSpawner, *claudeSubFakeConn) {
	t.Helper()
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spawns := 0
	spawner.onSpawn = func(conn *claudeSubFakeConn) {
		idx := spawns
		spawns++
		conn.mu.Lock()
		defer conn.mu.Unlock()
		if idx == 0 {
			conn.responder = live.respond
			return
		}
		conn.responder = claudeSubQuotaUsageResponder(probeUsage)
	}
	s, err := pool.acquire(context.Background(), key, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	return pool, spawner, spawner.call(t, 0).Conn
}

func TestClaudeSubUsageProbeOutcome(t *testing.T) {
	liveNotReady := claudeSubUsageNotReady("extra_usage missing")
	liveTransport := errors.New("claude CLI get_usage: usage unavailable")
	spawnErr := errors.New("spawn failed")
	tests := []struct {
		name     string
		liveErr  error
		probeErr error
		wantPass bool // the outcome authorizes the turn
		wantIs   error
		wantLive bool // the outcome keeps the live refusal's message and chain
	}{
		{name: "fresh confirmation passes", liveErr: liveNotReady, wantPass: true},
		{name: "no live refusal never authorizes", probeErr: spawnErr, wantIs: spawnErr},
		{name: "fresh state still unconfirmed keeps the live refusal", liveErr: liveNotReady, probeErr: claudeSubUsageNotReady("rate_limits_available=false"), wantIs: errClaudeSubUsageNotReady, wantLive: true},
		{name: "fresh transport failure keeps the live refusal", liveErr: liveNotReady, probeErr: errors.New("start claude CLI: boom"), wantIs: errClaudeSubUsageNotReady, wantLive: true},
		{name: "live transport failure with a fresh refusal stays unchanged", liveErr: liveTransport, probeErr: claudeSubUsageNotReady("extra_usage missing"), wantLive: true},
		{name: "fresh credits enabled refuses", liveErr: liveNotReady, probeErr: errClaudeSubUsageCreditsEnabled, wantIs: errClaudeSubUsageCreditsEnabled},
		{name: "fresh cancellation keeps its own error", liveErr: liveNotReady, probeErr: context.Canceled, wantIs: context.Canceled},
		{name: "fresh timeout keeps its own error", liveErr: liveNotReady, probeErr: context.DeadlineExceeded, wantIs: context.DeadlineExceeded},
		{name: "pool shutdown keeps the closed error", liveErr: liveNotReady, probeErr: errClaudeSubPoolClosed, wantIs: errClaudeSubPoolClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeSubUsageProbeOutcome(tc.liveErr, tc.probeErr)
			if tc.wantPass {
				if got != nil {
					t.Fatalf("claudeSubUsageProbeOutcome() = %v, want nil (confirmed off)", got)
				}
				return
			}
			if got == nil {
				t.Fatal("claudeSubUsageProbeOutcome() = nil, want a refusal")
			}
			if tc.wantIs != nil && !errors.Is(got, tc.wantIs) {
				t.Errorf("claudeSubUsageProbeOutcome() = %v, want %v", got, tc.wantIs)
			}
			if tc.wantLive {
				if got.Error() != tc.liveErr.Error() {
					t.Errorf("claudeSubUsageProbeOutcome() message = %q, want the live refusal %q", got.Error(), tc.liveErr.Error())
				}
				if !errors.Is(got, tc.liveErr) {
					t.Errorf("claudeSubUsageProbeOutcome() chain does not carry the live error %v", tc.liveErr)
				}
			}
		})
	}
}

// TestClaudeSubUsageProbeOutcomeCarriesProbeCause proves the fail-closed refusal
// keeps the live message while its chain carries why the confirmation failed.
func TestClaudeSubUsageProbeOutcomeCarriesProbeCause(t *testing.T) {
	liveErr := claudeSubUsageNotReady("extra_usage missing")
	probeErr := errors.New("start claude CLI: spawn failed")
	got := claudeSubUsageProbeOutcome(liveErr, probeErr)
	if got == nil {
		t.Fatal("claudeSubUsageProbeOutcome() = nil, want a fail-closed refusal")
	}
	if got.Error() != liveErr.Error() {
		t.Errorf("outcome message = %q, want the live refusal %q", got.Error(), liveErr.Error())
	}
	if !errors.Is(got, probeErr) {
		t.Errorf("outcome %v does not carry the probe error", got)
	}
	if !errors.Is(got, errClaudeSubUsageNotReady) {
		t.Errorf("outcome %v does not wrap errClaudeSubUsageNotReady", got)
	}
}

// TestClaudeSubConfirmUsageFresh proves one confirmation starts one process,
// applies the strict gate to its reply, and retires that process.
func TestClaudeSubConfirmUsageFresh(t *testing.T) {
	tests := []struct {
		name     string
		usage    json.RawMessage
		wantPass bool
		wantIs   error
		wantMsg  string
	}{
		{name: "credits off confirms", usage: claudeSubFixture(t, "get_usage_ok.json"), wantPass: true},
		{name: "credits on refuses", usage: claudeSubFixture(t, "get_usage_credits_on.json"), wantIs: errClaudeSubUsageCreditsEnabled, wantMsg: claudeSubCreditsEnabledWant},
		{name: "missing extra usage stays unconfirmed", usage: json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":3}}}`), wantIs: errClaudeSubUsageNotReady, wantMsg: claudeSubFailClosedWant + " (extra_usage missing)"},
		{name: "undecodable reply stays unconfirmed", usage: json.RawMessage(`"not-json"`), wantIs: errClaudeSubUsageNotReady, wantMsg: claudeSubFailClosedWant + " (undecodable usage response)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
			claudeSubUsageProbeReply(spawner, tc.usage)
			err := pool.confirmUsageFresh(context.Background())
			if tc.wantPass {
				if err != nil {
					t.Fatalf("confirmUsageFresh() = %v, want nil", err)
				}
			} else {
				if !errors.Is(err, tc.wantIs) {
					t.Fatalf("confirmUsageFresh() = %v, want %v", err, tc.wantIs)
				}
				if err.Error() != tc.wantMsg {
					t.Errorf("confirmUsageFresh() message = %q, want %q", err.Error(), tc.wantMsg)
				}
			}
			if n := spawner.callCount(); n != 1 {
				t.Fatalf("spawn count = %d, want exactly one confirmation process", n)
			}
			args := spawner.call(t, 0).Args
			if !claudeSubHasArg(args, "--no-session-persistence") {
				t.Errorf("probe args = %v, want --no-session-persistence", args)
			}
			if claudeSubHasArg(args, "--resume") {
				t.Errorf("probe args = %v, want no --resume", args)
			}
			if keys := claudeSubUsageProbeKeys(pool); len(keys) != 0 {
				t.Errorf("pool sessions = %v, want none: the probe must be retired", keys)
			}
			if !spawner.call(t, 0).Conn.isClosed() {
				t.Error("confirmation process was not closed")
			}
		})
	}
}

// TestClaudeSubConfirmUsageFreshStartsFreshProcessPerAttempt proves the pool
// never reuses a probe process: each confirmation is a brand-new CLI process.
func TestClaudeSubConfirmUsageFreshStartsFreshProcessPerAttempt(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	claudeSubUsageProbeReply(spawner, claudeSubFixture(t, "get_usage_ok.json"))
	for i := 1; i <= 2; i++ {
		if err := pool.confirmUsageFresh(context.Background()); err != nil {
			t.Fatalf("confirmation %d: %v", i, err)
		}
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want one fresh process per confirmation", n)
	}
	if spawner.call(t, 0).Conn == spawner.call(t, 1).Conn {
		t.Error("both confirmations used one process, want a fresh process each")
	}
	for i := 0; i < 2; i++ {
		if !spawner.call(t, i).Conn.isClosed() {
			t.Errorf("confirmation process %d was not closed", i)
		}
	}
	if keys := claudeSubUsageProbeKeys(pool); len(keys) != 0 {
		t.Errorf("pool sessions = %v, want none: the probes must be retired", keys)
	}
}

// TestClaudeSubConfirmUsageFreshRespectsSemaphore proves a confirmation waits
// for the pool's one slot instead of starting a second probe process.
func TestClaudeSubConfirmUsageFreshRespectsSemaphore(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.usageProbe.sem <- struct{}{}
	t.Cleanup(func() { <-pool.usageProbe.sem })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := pool.confirmUsageFresh(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("confirmUsageFresh() = %v, want context.Canceled", err)
	}
	if n := spawner.callCount(); n != 0 {
		t.Fatalf("spawn count = %d, want none while the slot is taken", n)
	}
}

// TestClaudeSubConfirmUsageFreshRefusesThroughShutdown proves pool shutdown while
// the slot is taken refuses with the closed error and starts no process.
func TestClaudeSubConfirmUsageFreshRefusesThroughShutdown(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.usageProbe.sem <- struct{}{}
	t.Cleanup(func() { <-pool.usageProbe.sem })
	if err := pool.Close(); err != nil {
		t.Fatalf("pool close: %v", err)
	}
	err := pool.confirmUsageFresh(context.Background())
	if !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("confirmUsageFresh() = %v, want %v", err, errClaudeSubPoolClosed)
	}
	if n := spawner.callCount(); n != 0 {
		t.Fatalf("spawn count = %d, want none after shutdown", n)
	}
}

// claudeSubWaitSpawnCount waits until the spawner has recorded want spawns.
func claudeSubWaitSpawnCount(t *testing.T, spawner *claudeSubTestSpawner, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for spawner.callCount() < want {
		if time.Now().After(deadline) {
			t.Fatalf("spawn count = %d, want %d", spawner.callCount(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestClaudeSubConfirmUsageFreshSpawnFailureReleasesSlot proves a failed probe
// startup leaves the failure visible, frees the pool slot, and does not poison
// the next confirmation.
func TestClaudeSubConfirmUsageFreshSpawnFailureReleasesSlot(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	claudeSubUsageProbeReply(spawner, claudeSubFixture(t, "get_usage_ok.json"))
	spawner.setErr(errors.New("spawn failed"))
	if err := pool.confirmUsageFresh(context.Background()); err == nil || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("confirmUsageFresh() = %v, want the spawn failure", err)
	}
	select {
	case pool.usageProbe.sem <- struct{}{}:
		<-pool.usageProbe.sem
	default:
		t.Fatal("confirmation kept the pool slot after a failed startup")
	}
	spawner.setErr(nil)
	if err := pool.confirmUsageFresh(context.Background()); err != nil {
		t.Fatalf("confirmation after the spawn recovered: %v", err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want the recovered confirmation's process", n)
	}
}

// TestClaudeSubConfirmUsageFreshRefusesThroughShutdownDuringProbe proves a pool
// shutdown while a confirmation is in flight reports the closed error and leaves
// no probe process behind.
func TestClaudeSubConfirmUsageFreshRefusesThroughShutdownDuringProbe(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	// The probe process never answers, so the confirmation is still in flight
	// when the pool closes.
	spawner.onSpawn = func(conn *claudeSubFakeConn) {
		conn.mu.Lock()
		conn.responder = func([]byte) []claudeSubEvent { return nil }
		conn.mu.Unlock()
	}
	errs := make(chan error, 1)
	go func() { errs <- pool.confirmUsageFresh(context.Background()) }()
	claudeSubWaitSpawnCount(t, spawner, 1)
	conn := spawner.call(t, 0).Conn
	deadline := time.Now().Add(2 * time.Second)
	for len(conn.sentLines()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("confirmation sent no get_usage request")
		}
		time.Sleep(time.Millisecond)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("pool close: %v", err)
	}
	select {
	case err := <-errs:
		if !errors.Is(err, errClaudeSubPoolClosed) {
			t.Fatalf("confirmUsageFresh() = %v, want %v", err, errClaudeSubPoolClosed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation did not return after pool shutdown")
	}
	if !conn.isClosed() {
		t.Error("probe process was not closed")
	}
}

// TestClaudeSubConfirmUsageFreshSerializes proves two confirmations never run
// their probe processes at the same time.
func TestClaudeSubConfirmUsageFreshSerializes(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	passing := claudeSubFixture(t, "get_usage_ok.json")
	block := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(block) }) }
	t.Cleanup(release)
	spawner.onSpawn = func(conn *claudeSubFakeConn) {
		conn.mu.Lock()
		conn.responder = claudeSubQuotaUsageResponder(passing)
		conn.sendBlock = block
		conn.mu.Unlock()
	}
	errs := make([]chan error, 2)
	errs[0] = make(chan error, 1)
	go func() { errs[0] <- pool.confirmUsageFresh(context.Background()) }()
	claudeSubWaitSpawnCount(t, spawner, 1)
	errs[1] = make(chan error, 1)
	go func() { errs[1] <- pool.confirmUsageFresh(context.Background()) }()
	// The second confirmation must wait for the first to finish its probe.
	time.Sleep(50 * time.Millisecond)
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d while the first confirmation runs, want 1", n)
	}
	release()
	for i, errc := range errs {
		select {
		case err := <-errc:
			if err != nil {
				t.Fatalf("confirmation %d: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("confirmation %d did not finish", i+1)
		}
	}
	if n := spawner.callCount(); n != 2 {
		t.Errorf("spawn count = %d, want one process per confirmation", n)
	}
}

// TestClaudeSubUsageGateProbeConfirmsTurn proves a live poll that cannot confirm
// the state is rescued by one fresh process: the turn then proceeds and writes
// its user line, while the probe process wrote no user content of its own.
func TestClaudeSubUsageGateProbeConfirmsTurn(t *testing.T) {
	claudeSubUsageGateFast(t, 30*time.Millisecond, time.Millisecond)
	claudeSubUsageProbeFast(t, time.Second)
	live := &claudeSubScript{
		usage:   json.RawMessage(`{"rate_limits_available":false}`),
		batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")},
	}
	pool, spawner, liveConn := claudeSubUsageProbeTurnPool(t, "probe-pass", live, claudeSubFixture(t, "get_usage_ok.json"))
	req := ChatRequest{TransportSession: "probe-pass", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("hello")}}
	var final ChatChunk
	if err := claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
		if chunk.Done {
			final = chunk
		}
		return nil
	}); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if final.Delta.Content != "Hello world" {
		t.Errorf("turn content = %q, want Hello world", final.Delta.Content)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want the live process plus one confirmation", n)
	}
	probe := spawner.call(t, 1).Conn
	if !probe.isClosed() {
		t.Error("confirmation process was not retired")
	}
	if got := claudeSubUsageProbeCount(probe, `"subtype":"get_usage"`); got != 1 {
		t.Errorf("confirmation get_usage requests = %d, want 1", got)
	}
	if got := claudeSubUsageProbeCount(probe, `"type":"user"`); got != 0 {
		t.Errorf("confirmation process user writes = %d, want none", got)
	}
	if got := claudeSubUsageProbeCount(liveConn, `"type":"user"`); got != 1 {
		t.Errorf("live user writes = %d, want 1 after the confirmation passed", got)
	}
	if keys := claudeSubUsageProbeKeys(pool); len(keys) != 1 || keys[0] != "probe-pass" {
		t.Errorf("pool sessions = %v, want only the live session", keys)
	}
}

// TestClaudeSubUsageGateProbeUnconfirmedRefuses proves an unconfirmable live
// state that one fresh process also cannot confirm stays fail-closed, keeps the
// original message and reason, and writes no user content. The live poll still
// makes several calls, yet exactly one probe process runs.
func TestClaudeSubUsageGateProbeUnconfirmedRefuses(t *testing.T) {
	claudeSubUsageGateFast(t, 30*time.Millisecond, time.Millisecond)
	claudeSubUsageProbeFast(t, time.Second)
	notReady := json.RawMessage(`{"rate_limits_available":false}`)
	pool, spawner, liveConn := claudeSubUsageProbeTurnPool(t, "probe-fail", &claudeSubScript{usage: notReady}, notReady)
	req := ChatRequest{TransportSession: "probe-fail", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("hello")}}
	err := claudeSubTurn(context.Background(), pool, req, func(ChatChunk) error { return nil })
	want := claudeSubFailClosedWant + " (rate_limits_available=false)"
	if err == nil || err.Error() != want {
		t.Fatalf("turn error = %v, want %q", err, want)
	}
	if !errors.Is(err, errClaudeSubUsageNotReady) {
		t.Errorf("turn error %v does not wrap errClaudeSubUsageNotReady", err)
	}
	var gateErr *claudeSubUsageGateError
	if !errors.As(err, &gateErr) {
		t.Error("refusal is not a usage-gate error")
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want the live process plus exactly one confirmation", n)
	}
	if got := claudeSubUsageProbeCount(liveConn, `"subtype":"get_usage"`); got < 2 {
		t.Errorf("live get_usage polls = %d, want the repeated backoff polls", got)
	}
	if got := claudeSubUsageProbeCount(spawner.call(t, 1).Conn, `"subtype":"get_usage"`); got != 1 {
		t.Errorf("confirmation get_usage requests = %d, want one per failing gate", got)
	}
	for _, line := range liveConn.sentLines() {
		if strings.Contains(line, `"type":"user"`) {
			t.Fatalf("refused turn wrote user content: %s", line)
		}
	}
}

// TestClaudeSubUsageGateProbeCreditsEnabledRefuses proves a fresh confirmation
// that reports credits enabled refuses with the definite credits error and
// writes no user content.
func TestClaudeSubUsageGateProbeCreditsEnabledRefuses(t *testing.T) {
	claudeSubUsageGateFast(t, 30*time.Millisecond, time.Millisecond)
	claudeSubUsageProbeFast(t, time.Second)
	notReady := json.RawMessage(`{"rate_limits_available":false}`)
	pool, spawner, liveConn := claudeSubUsageProbeTurnPool(t, "probe-fresh-credits", &claudeSubScript{usage: notReady}, claudeSubFixture(t, "get_usage_credits_on.json"))
	req := ChatRequest{TransportSession: "probe-fresh-credits", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("hello")}}
	err := claudeSubTurn(context.Background(), pool, req, func(ChatChunk) error { return nil })
	if err == nil || err.Error() != claudeSubCreditsEnabledWant {
		t.Fatalf("turn error = %v, want %q", err, claudeSubCreditsEnabledWant)
	}
	if !errors.Is(err, errClaudeSubUsageCreditsEnabled) {
		t.Errorf("turn error %v does not wrap errClaudeSubUsageCreditsEnabled", err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want the live process plus one confirmation", n)
	}
	if got := claudeSubUsageProbeCount(liveConn, `"type":"user"`); got != 0 {
		t.Errorf("live user writes = %d, want none", got)
	}
}

// TestClaudeSubUsageGateCreditsEnabledDoesNotProbe proves a definite
// credits-enabled refusal never starts a confirmation process.
func TestClaudeSubUsageGateCreditsEnabledDoesNotProbe(t *testing.T) {
	claudeSubUsageProbeFast(t, time.Second)
	live := &claudeSubScript{usage: claudeSubFixture(t, "get_usage_credits_on.json")}
	pool, spawner, _ := claudeSubUsageProbeTurnPool(t, "probe-credits", live, claudeSubFixture(t, "get_usage_ok.json"))
	req := ChatRequest{TransportSession: "probe-credits", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("hello")}}
	err := claudeSubTurn(context.Background(), pool, req, func(ChatChunk) error { return nil })
	if err == nil || err.Error() != claudeSubCreditsEnabledWant {
		t.Fatalf("turn error = %v, want %q", err, claudeSubCreditsEnabledWant)
	}
	if !errors.Is(err, errClaudeSubUsageCreditsEnabled) {
		t.Errorf("turn error %v does not wrap errClaudeSubUsageCreditsEnabled", err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want only the live process", n)
	}
}

// TestClaudeSubUsageGateTransportErrorDoesNotProbe proves a get_usage transport
// error returns without starting a confirmation process.
func TestClaudeSubUsageGateTransportErrorDoesNotProbe(t *testing.T) {
	claudeSubUsageProbeFast(t, time.Second)
	live := &claudeSubScript{usageErr: "usage unavailable"}
	pool, spawner, _ := claudeSubUsageProbeTurnPool(t, "probe-transport", live, claudeSubFixture(t, "get_usage_ok.json"))
	req := ChatRequest{TransportSession: "probe-transport", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("hello")}}
	err := claudeSubTurn(context.Background(), pool, req, func(ChatChunk) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "usage unavailable") {
		t.Fatalf("turn error = %v, want the get_usage transport error", err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want only the live process", n)
	}
}

// TestClaudeSubShouldRetryTurn proves a usage-gate refusal is never retried on a
// fresh process, while a pre-output failure with a resume target still is.
func TestClaudeSubShouldRetryTurn(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		emitted bool
		ctxErr  error
		resume  string
		want    bool
	}{
		{name: "pre-output failure with a resume target", err: errors.New("model unavailable"), resume: "sess-1", want: true},
		{name: "no error", resume: "sess-1"},
		{name: "after output", err: errors.New("model unavailable"), emitted: true, resume: "sess-1"},
		{name: "no resume target", err: errors.New("model unavailable")},
		{name: "caller cancelled", err: errors.New("model unavailable"), ctxErr: context.Canceled, resume: "sess-1"},
		{name: "unconfirmable usage gate refused after the confirmation", err: &claudeSubUsageGateError{err: claudeSubUsageNotReady("rate_limits_available=false")}, resume: "sess-1"},
		{name: "probe-reported credits enabled refusal is not retried", err: &claudeSubUsageGateError{err: errClaudeSubUsageCreditsEnabled}, resume: "sess-1"},
		{name: "probe timeout refusal is not retried", err: &claudeSubUsageGateError{err: context.DeadlineExceeded}, resume: "sess-1"},
		{name: "probe cancellation refusal is not retried", err: &claudeSubUsageGateError{err: context.Canceled}, resume: "sess-1"},
		{name: "bare live credits enabled refusal still retries", err: errClaudeSubUsageCreditsEnabled, resume: "sess-1", want: true},
		{name: "bare live transport failure still retries", err: errors.New("claude CLI get_usage: usage unavailable"), resume: "sess-1", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubShouldRetryTurn(tc.err, tc.emitted, tc.ctxErr, tc.resume); got != tc.want {
				t.Errorf("claudeSubShouldRetryTurn() = %v, want %v", got, tc.want)
			}
		})
	}
}
