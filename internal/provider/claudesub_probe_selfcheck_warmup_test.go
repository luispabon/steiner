//go:build claudesub_probe

package provider

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestClaudeSubProbeCallOutcome checks that a call's outcome comes only from the
// host's own state, never from the probe having resolved a handle.
func TestClaudeSubProbeCallOutcome(t *testing.T) {
	cases := []struct {
		name  string
		state claudeSubProbeCallState
		want  string
	}{
		{name: "waiting", state: claudeSubProbeCallState{ID: "a", Present: true, Waiting: true}, want: "waiting"},
		{name: "result stored", state: claudeSubProbeCallState{ID: "a", Present: true, HasResult: true}, want: "has result"},
		{name: "host closed", state: claudeSubProbeCallState{ID: "a", Present: true, HostClosed: true}, want: "closed by host teardown"},
		{name: "retired", state: claudeSubProbeCallState{ID: "a", Retired: true}, want: "retired"},
		{name: "absent", state: claudeSubProbeCallState{ID: "a"}, want: "absent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubProbeCallOutcome(tc.state); got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
	for _, outcome := range []string{"claimed", "consumed"} {
		if strings.Contains(claudeSubProbeCallOutcome(claudeSubProbeCallState{Retired: true}), outcome) {
			t.Fatalf("a retired call was reported as %q; delivery only comes from inbound text", outcome)
		}
	}
}

// TestClaudeSubProbeTurnAttribution checks that two open turns receive their own
// results in order, which is what E6's queued prompt depends on.
func TestClaudeSubProbeTurnAttribution(t *testing.T) {
	process, conn := claudeSubProbeTestProcess(t)
	rec := process.rec
	rec.beginTurn("PROBE-T1E6 interrupted turn")
	rec.beginTurn("PROBE-T2E6 queued turn")
	claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","result":"[Request interrupted by user]"}`)
	claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"success","stop_reason":"end_turn","result":"queued-ok"}`)

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec.mu.Lock()
		ready := len(process.rep.Turns) == 2 && process.rep.Turns[0].Result != nil && process.rep.Turns[1].Result != nil
		rec.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turns did not receive their own results: %+v", process.rep.Turns)
		}
		time.Sleep(claudeSubProbeHandlerPoll)
	}
	rec.mu.Lock()
	first, second := process.rep.Turns[0], process.rep.Turns[1]
	rec.mu.Unlock()
	if got := first.Result.TerminalReason; got != "aborted_streaming" {
		t.Fatalf("first turn terminal_reason = %q, want the interrupted result", got)
	}
	if got := second.Result.Text; got != "queued-ok" {
		t.Fatalf("second turn result text = %q, want the queued turn's own result", got)
	}
}

// TestClaudeSubProbePumpDeadline checks that a step timeout and an expired
// experiment deadline are reported as different things.
func TestClaudeSubProbePumpDeadline(t *testing.T) {
	process, _ := claudeSubProbeTestProcess(t)
	t.Run("step budget", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := process.pumpUntil(ctx, 50*time.Millisecond, "result", claudeSubProbeIsResult)
		if err == nil || !strings.Contains(err.Error(), "no result within 50ms") {
			t.Fatalf("pumpUntil() error = %v, want the step reason", err)
		}
	})
	t.Run("expired experiment context", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		<-ctx.Done()
		_, err := process.pumpUntil(ctx, 5*time.Second, "result", claudeSubProbeIsResult)
		if err == nil || !strings.Contains(err.Error(), "experiment deadline") {
			t.Fatalf("pumpUntil() error = %v, want the experiment deadline", err)
		}
	})
}

// TestClaudeSubProbePumpCLIExit checks that a wait ends as soon as the CLI exits
// instead of running out its step budget.
func TestClaudeSubProbePumpCLIExit(t *testing.T) {
	process, conn := claudeSubProbeTestProcess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = conn.Close(context.Background())
	}()
	started := time.Now()
	_, err := process.pumpUntil(ctx, 5*time.Second, "result", claudeSubProbeIsResult)
	if err == nil || !strings.Contains(err.Error(), "claude cli exited") {
		t.Fatalf("pumpUntil() error = %v, want the CLI exit", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("pumpUntil() waited %s after the CLI exited", elapsed)
	}
}

// TestClaudeSubProbeExperimentBounds checks every bound against the same step
// plan the experiment body consumes, and that the declared plans include the
// waits the bodies take.
func TestClaudeSubProbeExperimentBounds(t *testing.T) {
	experiments, err := claudeSubProbeExperiments(claudeSubProbeConfig{Model: claudeSubProbeDefaultModel})
	if err != nil {
		t.Fatalf("full experiment selection error = %v", err)
	}
	total := time.Duration(0)
	byName := map[string]claudeSubProbeExperiment{}
	for _, experiment := range experiments {
		byName[experiment.name] = experiment
		if experiment.steps == nil {
			t.Fatalf("%s has no step plan", experiment.name)
		}
		plan := experiment.steps.plan()
		if experiment.timeout < plan {
			t.Fatalf("%s bound %s is below its step plan %s", experiment.name, experiment.timeout, plan)
		}
		total += experiment.timeout
	}
	if total >= claudeSubProbeRunTimeout {
		t.Fatalf("summed experiment bounds %s reach the probe ceiling %s", total, claudeSubProbeRunTimeout)
	}
	wantE6 := claudeSubProbeInitWorstCase + 2*claudeSubProbeReadyTimeout + claudeSubProbeInterruptAck +
		2*claudeSubProbeResultTimeout + claudeSubProbeQueuedTail + claudeSubProbeTeardownBudget
	if got := byName["E6-queued-after-interrupt"].steps.plan(); got != wantE6 {
		t.Fatalf("E6 step plan = %s, want %s including the outbound-interrupt wait and one teardown", got, wantE6)
	}
	// E7 starts two persisted processes per trial, so each trial spends one init
	// gate, its resumed process's gate, four waits and two teardowns. The per-process
	// teardown term is what leaves room for only the default single trial in a full
	// run; an E7-only selection derives claudeSubProbeE7OnlyTrials.
	if claudeSubProbeCrashTrials != 1 {
		t.Fatalf("the full run plans %d E7 trials; only one fits the ceiling", claudeSubProbeCrashTrials)
	}
	wantE7 := claudeSubProbeCrashTrials * (2*claudeSubProbeInitWorstCase +
		claudeSubProbeCrashReply + claudeSubProbeCrashStream + claudeSubProbeCrashExit + claudeSubProbeCrashContinuation +
		2*claudeSubProbeTeardownBudget)
	if got := byName["E7-crash-resume-continue"].steps.plan(); got != wantE7 {
		t.Fatalf("E7 step plan = %s, want %s for %d trials", got, wantE7, claudeSubProbeCrashTrials)
	}
}

// TestClaudeSubProbeInitWarmup covers the warmup path: a CLI that reports init
// only after the first user input, a second prompt refused until init validates,
// and a missing init still failing.
func TestClaudeSubProbeInitWarmup(t *testing.T) {
	t.Run("missing init fails after the warmup", func(t *testing.T) {
		process, conn := claudeSubProbeTestProcess(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := claudeSubProbeGateInit(ctx, process, "", 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "init event after the gating turn") {
			t.Fatalf("gate error = %v, want the post-gating init failure", err)
		}
		if process.rep.InitPath != claudeSubProbeInitPathWarmup {
			t.Fatalf("init path = %q, want %q", process.rep.InitPath, claudeSubProbeInitPathWarmup)
		}
		sent := claudeSubProbeSentLines(conn)
		if len(sent) != 1 || !strings.Contains(string(sent[0]), "PROBE-WARMUP") {
			t.Fatalf("sent lines = %q, want exactly the warmup prompt", sent)
		}
		if err := process.sendPrompt("PROBE-T2 after a failed warmup"); err == nil {
			t.Fatal("sendPrompt() succeeded before init validated")
		}
	})

	t.Run("init after the warmup passes the gate", func(t *testing.T) {
		process, conn := claudeSubProbeTestProcess(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		refused := make(chan error, 1)
		go func() {
			claudeSubProbeWaitForSentLine(t, conn, "PROBE-WARMUP", 2*time.Second)
			refused <- process.sendPrompt("PROBE-T2 during the warmup")
			claudeSubProbePushEvent(t, conn, claudeSubProbeSelfCheckInit)
			claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"success","stop_reason":"end_turn","result":"ok"}`)
		}()
		if err := claudeSubProbeGateInit(ctx, process, "", 50*time.Millisecond, 3*time.Second, 3*time.Second); err != nil {
			t.Fatalf("gate error = %v, want a validated warmup path", err)
		}
		if err := <-refused; err == nil {
			t.Fatal("sendPrompt() succeeded during the warmup, before init validated")
		}
		if process.rep.InitPath != claudeSubProbeInitPathWarmup {
			t.Fatalf("init path = %q, want %q", process.rep.InitPath, claudeSubProbeInitPathWarmup)
		}
		if err := process.sendPrompt("PROBE-T3 after the gate"); err != nil {
			t.Fatalf("sendPrompt() after the gate error = %v", err)
		}
	})
}

// TestClaudeSubProbePersistedGateUsesFirstPrompt checks that a persisted spec
// gates on its own first prompt instead of writing a separate warmup turn.
func TestClaudeSubProbePersistedGateUsesFirstPrompt(t *testing.T) {
	process, conn := claudeSubProbeTestProcess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const firstPrompt = "PROBE-T1E5 Reply with exactly: alpha. Do not use tools."
	go func() {
		claudeSubProbeWaitForSentLine(t, conn, "PROBE-T1E5", 2*time.Second)
		claudeSubProbePushEvent(t, conn, claudeSubProbeSelfCheckInit)
		claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"success","stop_reason":"end_turn","result":"alpha"}`)
	}()
	if err := claudeSubProbeGateInit(ctx, process, firstPrompt, 50*time.Millisecond, 3*time.Second, 3*time.Second); err != nil {
		t.Fatalf("gate error = %v, want a validated first-prompt path", err)
	}
	if process.rep.InitPath != claudeSubProbeInitPathFirstPrompt {
		t.Fatalf("init path = %q, want %q", process.rep.InitPath, claudeSubProbeInitPathFirstPrompt)
	}
	if !process.gateConsumed {
		t.Fatal("the gate did not record that it consumed the experiment's first prompt")
	}
	sent := claudeSubProbeSentLines(conn)
	if len(sent) != 1 || !strings.Contains(string(sent[0]), "PROBE-T1E5") {
		t.Fatalf("sent lines = %q, want exactly the experiment's first prompt", sent)
	}
	if strings.Contains(string(sent[0]), "PROBE-WARMUP") {
		t.Fatal("a persisted process was sent the separate warmup prompt")
	}
	if err := process.sendPrompt("PROBE-T2 after the gate"); err != nil {
		t.Fatalf("sendPrompt() after the gate error = %v", err)
	}
}

// TestClaudeSubProbeGateKeepsRejectingUntilResult checks that the MCP rejecter is
// still active after init validates, up to the gating turn's result.
func TestClaudeSubProbeGateKeepsRejectingUntilResult(t *testing.T) {
	process, conn := claudeSubProbeTestProcess(t)
	host := newClaudeSubProbeFakeCallHost()
	process.callHost = host
	process.callSnapshot = func() []claudeSubProbeCallState {
		if process.rec.initGate() != nil {
			return nil
		}
		return []claudeSubProbeCallState{{ID: "toolu-gate", Present: true, Owner: true, Waiting: true}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		claudeSubProbeWaitForSentLine(t, conn, "PROBE-WARMUP", 2*time.Second)
		claudeSubProbePushEvent(t, conn, claudeSubProbeSelfCheckInit)
		// Give the rejecter a window in which init is already valid but the gating
		// result has not arrived.
		time.Sleep(200 * time.Millisecond)
		claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"success","stop_reason":"end_turn","result":"ok"}`)
	}()
	if err := claudeSubProbeGateInit(ctx, process, "", 50*time.Millisecond, 3*time.Second, 3*time.Second); err != nil {
		t.Fatalf("gate error = %v", err)
	}
	host.mu.Lock()
	result, ok := host.resolved["toolu-gate"]
	host.mu.Unlock()
	if !ok {
		t.Fatal("the rejecter stopped before the gating turn's result; the call was never rejected")
	}
	if !result.IsError {
		t.Fatalf("gating call resolved with %+v, want an error result", result)
	}
}

// claudeSubProbeFakeCallHost records what the gating rejecter resolves.
type claudeSubProbeFakeCallHost struct {
	mu       sync.Mutex
	calls    map[string]*claudeSubCall
	resolved map[string]claudeSubToolResult
}

func newClaudeSubProbeFakeCallHost() *claudeSubProbeFakeCallHost {
	return &claudeSubProbeFakeCallHost{
		calls:    map[string]*claudeSubCall{},
		resolved: map[string]claudeSubToolResult{},
	}
}

func (f *claudeSubProbeFakeCallHost) beginCall(id string) *claudeSubCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if call, ok := f.calls[id]; ok {
		return call
	}
	call := &claudeSubCall{id: id}
	f.calls[id] = call
	return call
}

func (f *claudeSubProbeFakeCallHost) resolve(call *claudeSubCall, result claudeSubToolResult) {
	if call == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved[call.id] = result
}

// TestClaudeSubProbeWarmupRejectsCalls checks that a tool call during a gating
// turn is resolved with an error result.
func TestClaudeSubProbeWarmupRejectsCalls(t *testing.T) {
	host := newClaudeSubProbeFakeCallHost()
	snapshot := func() []claudeSubProbeCallState {
		return []claudeSubProbeCallState{{ID: "toolu-warmup", Present: true, Owner: true, Waiting: true}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var recorded []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		claudeSubProbeRejectMCPCalls(ctx, snapshot, host, func(id string) { recorded = append(recorded, id) })
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		host.mu.Lock()
		result, ok := host.resolved["toolu-warmup"]
		host.mu.Unlock()
		if ok {
			if !result.IsError {
				t.Fatalf("gating call resolved with %+v, want an error result", result)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the gating rejecter resolved nothing")
		}
		time.Sleep(claudeSubProbeHandlerPoll)
	}
	cancel()
	<-done
	if len(recorded) != 1 || recorded[0] != "toolu-warmup" {
		t.Fatalf("recorded rejected calls = %v, want [toolu-warmup]", recorded)
	}
}

// TestClaudeSubProbeResumeUUIDByPrompt checks that the resume target comes from
// the turn whose prompt carries the marker, not from a turn index.
func TestClaudeSubProbeResumeUUIDByPrompt(t *testing.T) {
	process, conn := claudeSubProbeTestProcess(t)
	rec := process.rec
	rec.beginTurn("PROBE-T1E5 Reply with exactly: alpha. Do not use tools.")
	rec.beginTurn("PROBE-T2E5 Reply with exactly: beta. Do not use tools.")
	claudeSubProbePushEvent(t, conn, `{"type":"assistant","uuid":"uuid-t1","message":{"role":"assistant","content":[{"type":"text","text":"alpha"}]}}`)
	claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"success","stop_reason":"end_turn","result":"alpha"}`)
	claudeSubProbePushEvent(t, conn, `{"type":"assistant","uuid":"uuid-t2","message":{"role":"assistant","content":[{"type":"text","text":"beta"}]}}`)
	claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"success","stop_reason":"end_turn","result":"beta"}`)

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec.mu.Lock()
		ready := len(process.rep.Turns) == 2 && process.rep.Turns[0].Result != nil && process.rep.Turns[1].Result != nil
		rec.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turns did not complete: %+v", process.rep.Turns)
		}
		time.Sleep(claudeSubProbeHandlerPoll)
	}
	if got := process.lastAssistantUUIDForPrompt("PROBE-T2E5"); got != "uuid-t2" {
		t.Fatalf("resume uuid = %q, want uuid-t2", got)
	}
	if got := process.lastAssistantUUIDForPrompt("PROBE-T1E5"); got != "uuid-t1" {
		t.Fatalf("resume uuid = %q, want uuid-t1", got)
	}
	if got := process.lastAssistantUUIDForPrompt("PROBE-T9E5"); got != "" {
		t.Fatalf("resume uuid = %q, want an empty result for an absent marker", got)
	}
}

// TestClaudeSubProbeValidateArgv checks the load-deciding argv assertion.
func TestClaudeSubProbeValidateArgv(t *testing.T) {
	good := claudeSubArgs(claudeSubProbeSpawnOptions(claudeSubProbeDefaultModel, false, claudeSubResume{}))
	if err := claudeSubProbeValidateArgv(good); err != nil {
		t.Fatalf("claudeSubArgs() argv rejected: %v", err)
	}
	base := []string{"--strict-mcp-config", "--setting-sources=", "--tools", "", "--allowedTools", "mcp__steiner__*"}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing strict mcp config", args: []string{"--setting-sources=", "--tools", "", "--allowedTools", "mcp__steiner__*"}, want: "--strict-mcp-config"},
		{name: "missing setting sources", args: []string{"--strict-mcp-config", "--tools", "", "--allowedTools", "mcp__steiner__*"}, want: "--setting-sources"},
		{name: "setting sources not empty", args: []string{"--strict-mcp-config", "--setting-sources=project", "--tools", "", "--allowedTools", "mcp__steiner__*"}, want: "--setting-sources value"},
		{name: "tools not empty", args: []string{"--strict-mcp-config", "--setting-sources=", "--tools", "Bash", "--allowedTools", "mcp__steiner__*"}, want: "--tools value"},
		{name: "duplicate tools", args: append(append([]string{}, base...), "--tools", ""), want: "2 --tools switches"},
		{name: "duplicate allowed tools", args: append(append([]string{}, base...), "--allowedTools", "mcp__steiner__*"), want: "2 --allowedTools switches"},
		{name: "allowed tools missing", args: []string{"--strict-mcp-config", "--setting-sources=", "--tools", ""}, want: "--allowedTools"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := claudeSubProbeValidateArgv(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate error = %v, want %q", err, tc.want)
			}
		})
	}
}

// claudeSubProbeWaitForSentLine waits until the fake connection recorded an
// outbound line containing want.
func claudeSubProbeWaitForSentLine(t *testing.T, conn *claudeSubFakeConn, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, line := range claudeSubProbeSentLines(conn) {
			if strings.Contains(string(line), want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no outbound line containing %q within %s", want, timeout)
		}
		time.Sleep(claudeSubProbeHandlerPoll)
	}
}
