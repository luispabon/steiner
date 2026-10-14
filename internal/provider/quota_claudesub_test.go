package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/quota"
)

// claudeSubQuotaFixture returns the recorded get_usage reply of a Pro account.
func claudeSubQuotaFixture(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "claudesub", "get_usage_ok.json"))
	if err != nil {
		t.Fatalf("read usage fixture: %v", err)
	}
	return raw
}

// claudeSubQuotaUsageResponder answers get_usage with usage and every other
// control request with an empty success.
func claudeSubQuotaUsageResponder(usage json.RawMessage) func([]byte) []claudeSubEvent {
	return func(line []byte) []claudeSubEvent {
		var envelope struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &envelope) != nil || envelope.Type != "control_request" {
			return nil
		}
		if envelope.Request.Subtype == "get_usage" {
			return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, usage)}
		}
		return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, nil)}
	}
}

func TestParseClaudeSubUsageFixture(t *testing.T) {
	snap, err := parseClaudeSubUsage(claudeSubQuotaFixture(t))
	if err != nil {
		t.Fatalf("parseClaudeSubUsage: %v", err)
	}
	if snap.AccountKey != ClaudeSubQuotaAccountKey || snap.ProviderType != "claude_subscription" || snap.Plan != "pro" {
		t.Errorf("identity = %q/%q/%q, want claude_subscription/claude_subscription/pro", snap.AccountKey, snap.ProviderType, snap.Plan)
	}
	want := []struct {
		label    string
		duration time.Duration
		used     float64
		resets   string
	}{
		{"5h", 5 * time.Hour, 26, "2026-10-08T16:30:00.444533+00:00"},
		{"weekly", 7 * 24 * time.Hour, 4, "2026-10-13T22:00:00.444559+00:00"},
		{"weekly apps", 7 * 24 * time.Hour, 0, "2026-10-13T22:00:00.444559+00:00"},
	}
	if len(snap.Windows) != len(want) {
		t.Fatalf("windows = %d, want %d: %+v", len(snap.Windows), len(want), snap.Windows)
	}
	for i, w := range want {
		got := snap.Windows[i]
		resets, err := time.Parse(time.RFC3339, w.resets)
		if err != nil {
			t.Fatalf("parse expected resets %q: %v", w.resets, err)
		}
		if got.Label != w.label || got.Duration != w.duration || got.UsedPercent != w.used || !got.ResetsAt.Equal(resets) {
			t.Errorf("window %d = %+v, want label %q duration %v used %v resets %s", i, got, w.label, w.duration, w.used, w.resets)
		}
	}
}

// TestParseClaudeSubUsageWindowMapping proves the five mapped windows keep their
// order and labels, that null, missing, and utilization-less entries are
// skipped, and that unknown keys and a null resets_at do not fail the parse.
func TestParseClaudeSubUsageWindowMapping(t *testing.T) {
	raw := json.RawMessage(`{
		"subscription_type": "max",
		"rate_limits_available": true,
		"rate_limits": {
			"five_hour": {"utilization": 50, "resets_at": "2026-10-08T16:30:00Z"},
			"seven_day": null,
			"seven_day_opus": {"utilization": 12.5, "resets_at": null},
			"seven_day_sonnet": {"resets_at": "2026-10-13T22:00:00Z"},
			"seven_day_oauth_apps": {"utilization": 0, "resets_at": "2026-10-13T22:00:00Z"},
			"model_scoped": [],
			"extra_usage": {"is_enabled": true}
		}
	}`)
	snap, err := parseClaudeSubUsage(raw)
	if err != nil {
		t.Fatalf("parseClaudeSubUsage: %v", err)
	}
	if snap.Plan != "max" {
		t.Errorf("plan = %q, want max", snap.Plan)
	}
	wantLabels := []string{"5h", "weekly opus", "weekly apps"}
	wantUsed := []float64{50, 12.5, 0}
	if len(snap.Windows) != len(wantLabels) {
		t.Fatalf("windows = %+v, want labels %v", snap.Windows, wantLabels)
	}
	for i := range wantLabels {
		if got := snap.Windows[i]; got.Label != wantLabels[i] || got.UsedPercent != wantUsed[i] {
			t.Errorf("window %d = %q at %v, want %q at %v", i, got.Label, got.UsedPercent, wantLabels[i], wantUsed[i])
		}
	}
	if !snap.Windows[1].ResetsAt.IsZero() {
		t.Errorf("null resets_at gave %v, want zero time", snap.Windows[1].ResetsAt)
	}
}

func TestParseClaudeSubUsageRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"rate limits unavailable", `{"rate_limits_available":false,"rate_limits":{"five_hour":{"utilization":26}}}`},
		{"availability missing", `{"rate_limits":{"five_hour":{"utilization":26}}}`},
		{"rate_limits missing", `{"rate_limits_available":true}`},
		{"rate_limits null", `{"rate_limits_available":true,"rate_limits":null}`},
		{"invalid resets_at", `{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":1,"resets_at":"yesterday"}}}`},
		{"malformed json", `{"rate_limits_available":`},
		{"no recognized windows", `{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`},
		{"all recognized windows null", `{"rate_limits_available":true,"rate_limits":{"five_hour":null,"seven_day":null}}`},
		{"all recognized windows malformed", `{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":1,"resets_at":"yesterday"},"seven_day":7}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseClaudeSubUsage(json.RawMessage(tc.raw)); err == nil {
				t.Errorf("parseClaudeSubUsage(%s) error = nil, want error", tc.raw)
			}
		})
	}
}

// TestParseClaudeSubUsageSkipsMalformedWindows proves a mapped window with a
// malformed entry is skipped and the remaining windows still parse.
func TestParseClaudeSubUsageSkipsMalformedWindows(t *testing.T) {
	raw := json.RawMessage(`{
		"subscription_type": "pro",
		"rate_limits_available": true,
		"rate_limits": {
			"five_hour": {"utilization": 26, "resets_at": "2026-10-08T16:30:00Z"},
			"seven_day": {"utilization": 4, "resets_at": "not-a-time"},
			"seven_day_opus": 5,
			"seven_day_sonnet": [{"utilization": 1}],
			"seven_day_oauth_apps": {"utilization": "0", "resets_at": null}
		}
	}`)
	snap, err := parseClaudeSubUsage(raw)
	if err != nil {
		t.Fatalf("parseClaudeSubUsage: %v", err)
	}
	if len(snap.Windows) != 1 || snap.Windows[0].Label != "5h" || snap.Windows[0].UsedPercent != 26 {
		t.Errorf("windows = %+v, want only the 5h window at 26", snap.Windows)
	}
}

// TestParseClaudeSubUsageWeeklyLabel pins the seven_day label to the
// quota.WindowLabel of a seven-day duration.
func TestParseClaudeSubUsageWeeklyLabel(t *testing.T) {
	snap, err := parseClaudeSubUsage(json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"seven_day":{"utilization":4}}}`))
	if err != nil {
		t.Fatalf("parseClaudeSubUsage: %v", err)
	}
	want := quota.WindowLabel(7 * 24 * time.Hour)
	if len(snap.Windows) != 1 || snap.Windows[0].Label != want {
		t.Errorf("windows = %+v, want one window labelled %q", snap.Windows, want)
	}
}

func TestClaudeSubQuotaFetcherTimeout(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	tf, ok := NewClaudeSubQuotaFetcher(pool).(quota.TimeoutFetcher)
	if !ok {
		t.Fatal("fetcher does not implement quota.TimeoutFetcher")
	}
	if got := tf.FetchTimeout(); got != 20*time.Second {
		t.Errorf("FetchTimeout() = %v, want 20s", got)
	}
}

// TestClaudeSubQuotaFetchUsesIdleLiveSession proves Fetch reads usage through an
// idle live session without spawning and marks the snapshot as a fetch.
func TestClaudeSubQuotaFetchUsesIdleLiveSession(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "idle", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = claudeSubQuotaUsageResponder(claudeSubQuotaFixture(t))
	conn.mu.Unlock()

	snap, err := NewClaudeSubQuotaFetcher(pool).Fetch(context.Background(), quota.FetchOptions{AllowSpawn: false})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Source != quota.SourceFetch || snap.Plan != "pro" {
		t.Errorf("snapshot source = %q plan = %q, want fetch pro", snap.Source, snap.Plan)
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want 1 (idle session reused)", n)
	}
}

// TestClaudeSubQuotaLiveControlRunsWhileCallLockHeld proves a quota read uses a
// live session's control channel while another call holds that session's
// whole-call lock. The read must neither block nor spawn.
func TestClaudeSubQuotaLiveControlRunsWhileCallLockHeld(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "busy", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = claudeSubQuotaUsageResponder(claudeSubQuotaFixture(t))
	conn.mu.Unlock()

	type result struct {
		snap quota.Snapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snap, err := pool.QuotaSnapshot(context.Background(), false)
		done <- result{snap, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("QuotaSnapshot with call lock held: %v", res.err)
		}
		if res.snap.Plan != "pro" {
			t.Errorf("plan = %q, want pro", res.snap.Plan)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("QuotaSnapshot blocked behind the held session call lock")
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want 1 (live session reused)", n)
	}
	if !strings.Contains(strings.Join(conn.sentLines(), "\n"), `"subtype":"get_usage"`) {
		t.Error("no get_usage request was sent on the live session")
	}
}

func TestClaudeSubQuotaNoLiveSessionWithoutSpawn(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	_, err := pool.QuotaSnapshot(context.Background(), false)
	if err == nil || err.Error() != "no live claude session" {
		t.Errorf("QuotaSnapshot error = %v, want exactly %q", err, "no live claude session")
	}
	if n := spawner.callCount(); n != 0 {
		t.Errorf("spawn count = %d, want 0", n)
	}
}

// TestClaudeSubQuotaProbeSpawnsAndStaysPooled proves allowSpawn starts one
// nonpersistent probe with no tools and no turn cap, that the probe stays pooled
// after the read, and that a later read without spawn uses it.
func TestClaudeSubQuotaProbeSpawnsAndStaysPooled(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	usage := claudeSubQuotaFixture(t)
	spawner.onSpawn = func(conn *claudeSubFakeConn) {
		conn.responder = claudeSubQuotaUsageResponder(usage)
	}
	snap, err := pool.QuotaSnapshot(context.Background(), true)
	if err != nil {
		t.Fatalf("QuotaSnapshot with allowSpawn: %v", err)
	}
	if snap.Plan != "pro" {
		t.Errorf("plan = %q, want pro", snap.Plan)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1", n)
	}
	call := spawner.call(t, 0)
	if claudeSubHasArg(call.Args, "--mcp-config") {
		t.Error("probe spawned with an MCP config, want no tools")
	}
	if !claudeSubHasArg(call.Args, "--no-session-persistence") {
		t.Error("probe persists its session, want --no-session-persistence")
	}
	if claudeSubHasArg(call.Args, "--max-turns") {
		t.Error("probe sets --max-turns, want no turn cap")
	}
	if claudeSubHasArg(call.Args, "--model") {
		t.Error("probe passes --model, want the CLI default model")
	}
	if call.Conn.isClosed() {
		t.Fatal("probe closed after the read, want it kept in the pool")
	}
	if claudeSubQuotaProbeKey != "quota-probe" {
		t.Errorf("probe key = %q, want quota-probe", claudeSubQuotaProbeKey)
	}
	pool.mu.Lock()
	_, pooled := pool.sessions[claudeSubQuotaProbeKey]
	pool.mu.Unlock()
	if !pooled {
		t.Fatal("probe session is not in the pool")
	}
	if _, err := pool.QuotaSnapshot(context.Background(), false); err != nil {
		t.Fatalf("QuotaSnapshot without spawn after probe: %v", err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count after pooled read = %d, want 1", n)
	}
}

// TestClaudeSubArgsModelFlag proves an empty model omits --model and a set model
// passes it through.
func TestClaudeSubArgsModelFlag(t *testing.T) {
	if claudeSubHasArg(claudeSubArgs(claudeSubSpawnOptions{}), "--model") {
		t.Error("empty model passes --model, want the CLI default")
	}
	got, ok := claudeSubArgValue(claudeSubArgs(claudeSubSpawnOptions{Model: "claude-haiku-5-5"}), "--model")
	if !ok || got != "claude-haiku-5-5" {
		t.Errorf("--model = %q (present %v), want claude-haiku-5-5", got, ok)
	}
}

// TestClaudeSubQuotaObserverGateTee proves the usage gate reports a successful,
// parsable get_usage reply once as a passive snapshot, that a nil observer is
// safe, and that an unparsable reply is not observed and still fails the gate
// closed.
func TestClaudeSubQuotaObserverGateTee(t *testing.T) {
	claudeSubUsageGateFast(t, 30*time.Millisecond, time.Millisecond)
	passing := claudeSubQuotaFixture(t)
	tests := []struct {
		name         string
		usage        json.RawMessage
		observe      bool
		wantObserved int
		wantErr      string
	}{
		{name: "passing reply observed once", usage: passing, observe: true, wantObserved: 1},
		{name: "nil observer", usage: passing},
		{name: "unparsable reply not observed and fails closed", usage: json.RawMessage(`{"rate_limits_available":false}`), observe: true, wantErr: "fail closed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var observed []quota.Snapshot
			opts := ClaudeSubscriptionPoolOptions{}
			if tc.observe {
				opts.QuotaObserver = func(snap quota.Snapshot) { observed = append(observed, snap) }
			}
			pool, spawner := newClaudeSubTestPool(t, opts)
			s, err := pool.acquire(context.Background(), "tee", claudeSubTestSpec())
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			pool.release(s)
			conn := spawner.call(t, 0).Conn
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
					if envelope.Request.Subtype == "get_usage" {
						return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, tc.usage)}
					}
					return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, nil)}
				case "user":
					for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl") {
						conn.push(ev)
					}
				}
				return nil
			}
			conn.mu.Unlock()

			req := ChatRequest{TransportSession: "tee", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("hello")}}
			err = claudeSubTurn(context.Background(), pool, req, func(ChatChunk) error { return nil })
			if tc.wantErr == "" && err != nil {
				t.Fatalf("turn: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("turn error = %v, want %q", err, tc.wantErr)
			}
			if len(observed) != tc.wantObserved {
				t.Fatalf("observed snapshots = %d, want %d", len(observed), tc.wantObserved)
			}
			if tc.wantObserved > 0 && (observed[0].Source != quota.SourcePassive || observed[0].Plan != "pro") {
				t.Errorf("observed snapshot source = %q plan = %q, want passive pro", observed[0].Source, observed[0].Plan)
			}
		})
	}
}
