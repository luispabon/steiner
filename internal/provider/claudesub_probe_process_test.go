//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Step budgets of one probe experiment. Their worst-case sum, including the
// production control timeout for an interrupt, stays inside each experiment's
// own bound (see claudeSubProbeExperiments and the self-check that pins it).
const (
	claudeSubProbeInitBeforeInput = 10 * time.Second
	claudeSubProbeWarmupResult    = 20 * time.Second
	claudeSubProbeInitAfterWarmup = 20 * time.Second
	claudeSubProbeReadyTimeout    = 20 * time.Second
	claudeSubProbeResultTimeout   = 20 * time.Second
	claudeSubProbeTailWindow      = 3 * time.Second
	claudeSubProbeInterruptAck    = 30 * time.Second
	claudeSubProbeHandlerPoll     = 25 * time.Millisecond

	// claudeSubProbeTeardownBudget is the per-process budget the step plan charges
	// for retiring a process and closing its pool through the production paths,
	// which no wait covers. The coordinator spends at most two claudeSubCloseGrace
	// waits (the child's own exit after stdin EOF, then the readers' drain after
	// tree termination) around its terminate and wait steps, so the budget is that
	// sum plus claudeSubProbeTeardownMargin.
	claudeSubProbeTeardownMargin = 2 * time.Second

	// claudeSubProbeInitWorstCase is the worst case of the init gate: no init
	// before the first input, a gating turn that spends its whole result budget,
	// then the post-gating init budget.
	claudeSubProbeInitWorstCase = claudeSubProbeInitBeforeInput + claudeSubProbeWarmupResult + claudeSubProbeInitAfterWarmup

	claudeSubProbeWarmupPrompt        = "PROBE-WARMUP: reply with the single word ok and do not call any tool."
	claudeSubProbeInitPathBeforeInput = "init before input"
	claudeSubProbeInitPathWarmup      = "init after the separate warmup turn"
	claudeSubProbeInitPathFirstPrompt = "init after the experiment's first prompt"
)

// claudeSubProbeTeardownBudget is the per-process teardown term claudeSubProbeSteps
// plan charges: the production coordinator's two close-grace waits plus a margin for
// its terminate, reap and cleanup steps.
var claudeSubProbeTeardownBudget = 2*claudeSubCloseGrace + claudeSubProbeTeardownMargin

// claudeSubProbeCallHost is the MCP host subset the gating rejection uses. The
// production host satisfies it; the self-check supplies a fake.
type claudeSubProbeCallHost interface {
	beginCall(id string) *claudeSubCall
	resolve(call *claudeSubCall, result claudeSubToolResult)
}

// claudeSubProbeCallState is one MCP host call as the probe observed it.
type claudeSubProbeCallState struct {
	ID         string `json:"id"`
	Present    bool   `json:"present"`
	Registered bool   `json:"registered"`
	Owner      bool   `json:"owner"`
	Waiting    bool   `json:"waiting"`
	HasResult  bool   `json:"has_result"`
	Retired    bool   `json:"retired"`
	HostClosed bool   `json:"host_closed"`
	Outcome    string `json:"outcome"`
}

// claudeSubProbeCallSample is the host call state at one labelled moment.
type claudeSubProbeCallSample struct {
	AtMS  int64                     `json:"at_ms"`
	Label string                    `json:"label"`
	Calls []claudeSubProbeCallState `json:"calls"`
}

// claudeSubProbeProcess is one CLI process started through the production pool
// path, with its recorder attached.
type claudeSubProbeProcess struct {
	key  string
	pool *ClaudeSubscriptionPool
	sess *claudeSubSession
	tee  *claudeSubProbeTeeConn
	rec  *claudeSubProbeRecorder
	rep  *claudeSubProbeProcessReport

	// callHost and callSnapshot expose the MCP host the gating revert uses; both
	// are nil for a tool-less session and are replaceable by a self-check.
	callHost     claudeSubProbeCallHost
	callSnapshot func() []claudeSubProbeCallState

	// gateConsumed records that the init gate used the experiment's own first
	// prompt as its gating turn, so the body must not send it again.
	gateConsumed bool
	// gateBlocks, when set, is the exact user line the gate writes as that first
	// prompt. E7's continuation line carries two text blocks, so the gating prompt
	// text alone is not its wire form.
	gateBlocks []map[string]any

	teardownOnce sync.Once
	teardownErr  error
}

// claudeSubProbeStartProcess starts one CLI process the production way: a pool
// with the probe's private cwd, then acquire, which builds argv from
// claudeSubArgs and the child environment from claudeSubSessionEnv. Only the
// spawn seam is wrapped, to validate the argv, and to record the exact argv,
// environment shape, argv switches and every exchanged line. The returned
// process has passed the init gate, so it is the only point from which an
// ordinary prompt may be written.
func claudeSubProbeStartProcess(ctx context.Context, cfg claudeSubProbeConfig, rep *claudeSubProbeProcessReport, key string, spec claudeSubStartSpec, gatingPrompt string, gatingBlocks []map[string]any, gatingResult time.Duration) (*claudeSubProbeProcess, error) {
	rec := newClaudeSubProbeRecorder(rep, cfg.Model)
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{
		WorkDir:            cfg.WorkDir,
		ToolOutputMaxBytes: claudeSubProbeToolOutputMaxBytes,
	})
	var spawned *claudeSubProbeTeeConn
	pool.spawn = func(ctx context.Context, path string, args, env []string, dir string) (claudeSubConn, error) {
		if err := claudeSubProbeValidateArgv(args); err != nil {
			return nil, err
		}
		conn, err := spawnClaudeSubProcess(ctx, path, args, env, dir)
		if err != nil {
			return nil, err
		}
		rep.Argv = append([]string{path}, args...)
		rep.Flags = claudeSubProbeFlagSummary(claudeSubProbeArgvFlags(args))
		rep.Dir = dir
		rep.Env = claudeSubProbeEnvSummary(env)
		spawned = newClaudeSubProbeTeeConn(conn, rec)
		return spawned, nil
	}
	sess, err := pool.acquire(ctx, key, spec)
	if err != nil {
		// acquire tore down any partially started session itself, so the pool holds
		// no session here; closing it only stops the reaper goroutine. A spawned
		// tee still owns a forwarding goroutine, which is shut down explicitly.
		if spawned != nil {
			spawned.shutdown()
		}
		rec.close()
		_ = pool.Close()
		return nil, err
	}
	tee := spawned
	process := &claudeSubProbeProcess{key: key, pool: pool, sess: sess, tee: tee, rec: rec, rep: rep, gateBlocks: gatingBlocks}
	if sess.host != nil {
		process.callHost = sess.host
		process.callSnapshot = func() []claudeSubProbeCallState { return claudeSubProbeHostCalls(sess.host) }
	}
	if err := claudeSubProbeGateInit(ctx, process, gatingPrompt, claudeSubProbeInitBeforeInput, claudeSubProbeInitAfterWarmup, claudeSubProbeGateResultBudget(gatingResult)); err != nil {
		if closeErr := process.teardown(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close probe process after a failed init gate: %w", closeErr))
		}
		return nil, err
	}
	return process, nil
}

// claudeSubProbeGateInit settles the process's init event. When init arrives
// before input it is validated directly. Otherwise the probe sends one gating
// turn and holds every other prompt until init validates. The gating turn is the
// probe's fixed warmup prompt for an ephemeral process, and the experiment's own
// first tool-free prompt for a persisted or resumed one, so no extra turn enters
// a stored transcript. While the gating turn runs, and until its result arrives,
// every MCP tool call is resolved with an error result.
func claudeSubProbeGateInit(ctx context.Context, p *claudeSubProbeProcess, gatingPrompt string, beforeInput, afterGating, gatingResult time.Duration) error {
	_, err := p.pumpUntil(ctx, beforeInput, "init event", claudeSubProbeIsInit)
	if err == nil {
		if err := p.rec.initGate(); err != nil {
			return fmt.Errorf("probe init gate: %w", err)
		}
		p.rep.InitPath = claudeSubProbeInitPathBeforeInput
		return nil
	}
	if fatal := p.rec.fatalErr(); fatal != nil {
		return fmt.Errorf("probe init gate: %w", fatal)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("probe init gate: %w", ctxErr)
	}

	prompt := gatingPrompt
	if prompt == "" {
		prompt = claudeSubProbeWarmupPrompt
		p.rep.InitPath = claudeSubProbeInitPathWarmup
	} else {
		p.rep.InitPath = claudeSubProbeInitPathFirstPrompt
	}
	p.rec.beginTurn(prompt)
	blocks := p.gateBlocks
	if len(blocks) == 0 {
		blocks = claudeSubUserBlocks([]Message{{Role: MessageRoleUser, Content: prompt}})
	}
	if err := p.sendRawBlocks(blocks); err != nil {
		return err
	}
	p.gateConsumed = gatingPrompt != ""

	rejectCtx, stopReject := context.WithCancel(ctx)
	rejectDone := make(chan struct{})
	go func() {
		defer close(rejectDone)
		claudeSubProbeRejectMCPCalls(rejectCtx, p.callSnapshot, p.callHost, func(id string) {
			p.rep.WarmupRejectedCalls = append(p.rep.WarmupRejectedCalls, id)
		})
	}()
	// The rejecter stays active until the gating turn's result is in, not just
	// until init validates, so a tool call the gating turn issues after init
	// cannot do work either.
	defer func() {
		stopReject()
		<-rejectDone
	}()

	var sawResult bool
	_, err = p.pumpUntil(ctx, afterGating, "init event after the gating turn", func(entry claudeSubProbeEntry) bool {
		if claudeSubProbeIsResult(entry) {
			sawResult = true
		}
		return claudeSubProbeIsInit(entry)
	})
	if err != nil {
		if fatal := p.rec.fatalErr(); fatal != nil {
			return fmt.Errorf("probe init gate: %w", fatal)
		}
		return fmt.Errorf("probe init gate: %w", err)
	}
	if err := p.rec.initGate(); err != nil {
		return fmt.Errorf("probe init gate: %w", err)
	}
	if !sawResult {
		if _, err := p.pumpUntil(ctx, gatingResult, "gating turn result", claudeSubProbeIsResult); err != nil {
			return fmt.Errorf("probe init gate: %w", err)
		}
	}
	return nil
}

// claudeSubProbeGateResultBudget is the budget the init gate gives the gating
// turn's result: the experiment's declared budget when it has one, otherwise the
// probe's fixed gating-result budget. E7 passes its declared continuation budget
// so the gate and the step plan charge the same time for the same turn.
func claudeSubProbeGateResultBudget(declared time.Duration) time.Duration {
	if declared > 0 {
		return declared
	}
	return claudeSubProbeWarmupResult
}

// claudeSubProbeRejectMCPCalls resolves every live MCP call with an error result
// until ctx ends. It keeps a gating turn from doing tool work before init is
// validated, and reports each rejected call id.
func claudeSubProbeRejectMCPCalls(ctx context.Context, snapshot func() []claudeSubProbeCallState, host claudeSubProbeCallHost, record func(string)) {
	if host == nil || snapshot == nil {
		return
	}
	ticker := time.NewTicker(claudeSubProbeHandlerPoll)
	defer ticker.Stop()
	rejected := map[string]struct{}{}
	for {
		for _, state := range snapshot() {
			if !state.Present {
				continue
			}
			if _, ok := rejected[state.ID]; ok {
				continue
			}
			call := host.beginCall(state.ID)
			if call == nil {
				continue
			}
			host.resolve(call, claudeSubToolResult{Text: "probe gating turn rejected this tool call: init is not validated", IsError: true})
			rejected[state.ID] = struct{}{}
			record(state.ID)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// teardown closes the process through the production retire path, records the
// exit evidence, and stops the pool. It runs once and returns the session's
// teardown result.
func (p *claudeSubProbeProcess) teardown() error {
	p.teardownOnce.Do(func() {
		if p.sess == nil {
			return
		}
		exit := &claudeSubProbeExit{ExitedBeforeTeardown: p.sess.exited()}
		p.pool.retireAndRelease(p.key, p.sess)
		exit.ProcErr = claudeSubProbeErrorString(p.sess.procErr)
		exit.ConnErr = claudeSubProbeErrorString(p.sess.conn.Err())
		if p.tee != nil {
			exit.Status, exit.WaitErr, exit.StatusKnown = claudeSubProbeExitStatus(p.tee.claudeSubConn)
			p.tee.shutdown()
		}
		exit.PoolCloseErr = claudeSubProbeErrorString(p.pool.Close())
		exit.Unobserved = p.rec.drainPending()
		exit.AtMS = p.rec.at()
		p.rep.Exit = exit
		p.rec.close()
		p.teardownErr = p.sess.procErr
		p.rep.TeardownErr = claudeSubProbeErrorString(p.teardownErr)
	})
	return p.teardownErr
}

// claudeSubProbeExitStatus reports the reaped child's exit status and wait error.
// The production lifecycle coordinator reaps the child before it publishes its
// terminal state, and session teardown waits for that state, so the status is
// known only once that terminal channel is closed. A bounded teardown that gave
// up leaves it unknown rather than reporting a false 0.
func claudeSubProbeExitStatus(conn claudeSubConn) (int, string, bool) {
	proc, ok := conn.(*claudeSubProcess)
	if !ok {
		return 0, "", false
	}
	select {
	case <-proc.terminal:
	default:
		return 0, "", false
	}
	proc.errMu.Lock()
	waitErr := proc.waitErr
	proc.errMu.Unlock()
	if waitErr == nil {
		return 0, "", true
	}
	return claudeSubExitCode(waitErr), claudeSubProbeErrorString(waitErr), true
}

// sendPrompt writes one synthetic user turn through the production writer. It
// refuses until the process's init event has been validated.
func (p *claudeSubProbeProcess) sendPrompt(prompt string) error {
	if err := p.rec.initGate(); err != nil {
		return fmt.Errorf("send probe prompt: %w", err)
	}
	return p.sendRawPrompt(prompt)
}

// sendRawPrompt writes one synthetic user turn without consulting the init gate.
// Only the gating turn, which exists to make the CLI report init, uses it.
func (p *claudeSubProbeProcess) sendRawPrompt(prompt string) error {
	return p.sendRawBlocks(claudeSubUserBlocks([]Message{{Role: MessageRoleUser, Content: prompt}}))
}

// sendRawBlocks writes one user line of pre-built content blocks through the
// production writer, without consulting the init gate.
func (p *claudeSubProbeProcess) sendRawBlocks(blocks []map[string]any) error {
	if err := writeClaudeSubUser(p.sess.conn, blocks); err != nil {
		return fmt.Errorf("send probe prompt: %w", err)
	}
	return nil
}

func (p *claudeSubProbeProcess) beginTurn(prompt string) (*claudeSubProbeTurn, error) {
	turn := p.rec.beginTurn(prompt)
	if err := p.sendPrompt(prompt); err != nil {
		return nil, err
	}
	return turn, nil
}

func (p *claudeSubProbeProcess) runTurn(ctx context.Context, prompt string, timeout time.Duration) error {
	if _, err := p.beginTurn(prompt); err != nil {
		return err
	}
	return p.awaitResult(ctx, timeout)
}

// awaitResult waits for a terminal result.
func (p *claudeSubProbeProcess) awaitResult(ctx context.Context, timeout time.Duration) error {
	_, err := p.pumpUntil(ctx, timeout, "result", claudeSubProbeIsResult)
	return err
}

// awaitInterruptedResult waits for the result of a turn the probe interrupted.
func (p *claudeSubProbeProcess) awaitInterruptedResult(ctx context.Context, timeout time.Duration) error {
	_, err := p.pumpUntil(ctx, timeout, "result after interrupt", claudeSubProbeIsResult)
	return err
}

func (p *claudeSubProbeProcess) interrupt(ctx context.Context) error {
	if _, err := p.sess.control.interrupt(ctx); err != nil {
		return fmt.Errorf("send probe interrupt: %w", err)
	}
	return nil
}

// claudeSubProbeAwaitInterrupt waits for the interrupt request to settle: its
// control response, its error, or the control timeout.
func claudeSubProbeAwaitInterrupt(done <-chan error, timeout time.Duration) error {
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("probe interrupt control request did not settle within %s", timeout)
	}
}

// awaitOutboundInterrupt waits until the interrupt request line has been written,
// without waiting for its control response.
func (p *claudeSubProbeProcess) awaitOutboundInterrupt(ctx context.Context, since int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if p.rec.outboundInterruptSince(since) >= 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("probe interrupt request was not written within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(claudeSubProbeHandlerPoll):
		}
	}
}

// pumpUntil waits up to timeout for an event the stop predicate matches. A
// timeout is reported as the step's own reason while the experiment still has
// time left, and as the experiment deadline once the parent context expired. A
// CLI exit or a closed event stream ends the wait at once.
func (p *claudeSubProbeProcess) pumpUntil(ctx context.Context, timeout time.Duration, what string, stop func(claudeSubProbeEntry) bool) (claudeSubProbeEntry, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	entry, ok, err := p.rec.pump(waitCtx, stop)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return claudeSubProbeEntry{}, fmt.Errorf("probe experiment deadline reached while waiting for %s: %w", what, ctxErr)
			}
			return claudeSubProbeEntry{}, fmt.Errorf("no %s within %s", what, timeout)
		}
		return claudeSubProbeEntry{}, err
	}
	if !ok {
		return claudeSubProbeEntry{}, errClaudeSubProbeEnded
	}
	return entry, nil
}

// tail records what arrives during window after the caller's last observation.
// A window that ends with nothing new is the expected outcome.
func (p *claudeSubProbeProcess) tail(ctx context.Context, window time.Duration) {
	tailCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	_, _, _ = p.rec.pump(tailCtx, nil) // the window deadline ends the drain
}

// sampleMCP records the MCP host's call state at one labelled moment.
func (p *claudeSubProbeProcess) sampleMCP(label string) {
	p.rep.MCPCalls = append(p.rep.MCPCalls, claudeSubProbeCallSample{
		AtMS:  p.rec.at(),
		Label: label,
		Calls: claudeSubProbeHostCalls(p.sess.host),
	})
}

// hostCallState reports the state of one call id, including retired ids that are
// no longer in the live set.
func (p *claudeSubProbeProcess) hostCallState(id string) claudeSubProbeCallState {
	for _, state := range claudeSubProbeHostCalls(p.sess.host) {
		if state.ID == id {
			return state
		}
	}
	if p.sess.host == nil {
		return claudeSubProbeCallState{ID: id}
	}
	closed, retired := claudeSubProbeHostFlags(p.sess.host, id)
	state := claudeSubProbeCallState{ID: id, HostClosed: closed, Retired: retired}
	state.Outcome = claudeSubProbeCallOutcome(state)
	return state
}

// claudeSubProbeCallOutcome names what the host's own state says about one call.
// It never infers delivery from the probe resolving a handle: resolving a retired
// call discards the result, so whether the model saw it comes from the next
// turn's inbound text instead.
func claudeSubProbeCallOutcome(state claudeSubProbeCallState) string {
	switch {
	case state.Present && state.Waiting:
		return "waiting"
	case state.Present && state.HasResult:
		return "has result"
	case state.HostClosed:
		return "closed by host teardown"
	case state.Retired:
		return "retired"
	default:
		return "absent"
	}
}

// claudeSubProbeHostCalls snapshots the live MCP calls of host.
func claudeSubProbeHostCalls(host *claudeSubMCPHost) []claudeSubProbeCallState {
	if host == nil {
		return nil
	}
	closed, retired := claudeSubProbeHostState(host)
	host.mu.Lock()
	defer host.mu.Unlock()
	out := make([]claudeSubProbeCallState, 0, len(host.calls))
	for id, call := range host.calls {
		state := claudeSubProbeCallState{
			ID:         id,
			Present:    true,
			Registered: call.registered,
			Owner:      call.owner,
			Waiting:    call.waiter != nil,
			HasResult:  call.result != nil,
			Retired:    call.done,
			HostClosed: closed,
		}
		if _, ok := retired[id]; ok {
			state.Retired = true
		}
		state.Outcome = claudeSubProbeCallOutcome(state)
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// claudeSubProbeHostFlags reports the host's closed state and whether id is in
// its retired set.
func claudeSubProbeHostFlags(host *claudeSubMCPHost, id string) (bool, bool) {
	closed, retired := claudeSubProbeHostState(host)
	_, wasRetired := retired[id]
	return closed, wasRetired
}

// claudeSubProbeHostState copies the host's closed flag and retired id set.
func claudeSubProbeHostState(host *claudeSubMCPHost) (bool, map[string]struct{}) {
	host.mu.Lock()
	defer host.mu.Unlock()
	retired := make(map[string]struct{}, len(host.retired))
	for id := range host.retired {
		retired[id] = struct{}{}
	}
	return host.closed, retired
}

// claudeSubProbeAwaitMCPHandler waits until a tools/call handler owns and blocks
// on the call for id, which is window B of the interrupt experiments.
func claudeSubProbeAwaitMCPHandler(ctx context.Context, p *claudeSubProbeProcess, id string, timeout time.Duration) error {
	if p.sess.host == nil {
		return errors.New("probe session has no MCP host")
	}
	deadline := time.Now().Add(timeout)
	for {
		if state := p.hostCallState(id); state.Owner && state.Waiting {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("probe MCP handler for call %q did not park within %s", id, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(claudeSubProbeHandlerPoll):
		}
	}
}

func (p *claudeSubProbeProcess) timelineLen() int {
	return p.rec.timelineLen()
}

func (p *claudeSubProbeProcess) turn(index int) *claudeSubProbeTurn {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	if index < 0 || index >= len(p.rep.Turns) {
		return nil
	}
	return p.rep.Turns[index]
}

// lastAssistantUUIDForPrompt returns the last assistant echo uuid of the turn
// whose prompt contains marker, which is how E5 picks a resume target without
// depending on turn indexes.
func (p *claudeSubProbeProcess) lastAssistantUUIDForPrompt(marker string) string {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	for _, turn := range p.rep.Turns {
		if strings.Contains(turn.Prompt, marker) {
			return turn.LastAssistantUUID
		}
	}
	return ""
}

func (p *claudeSubProbeProcess) sessionID() string {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return p.rep.SessionID
}

// resultsAfter returns the results recorded at or after index.
func (p *claudeSubProbeProcess) resultsAfter(index int) []claudeSubProbeEntry {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return claudeSubProbeResultsIn(p.rep.Timeline, index)
}

// resultsWithTextAfter returns the results after index whose text names want.
func (p *claudeSubProbeProcess) resultsWithTextAfter(index int, want string) []claudeSubProbeEntry {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return claudeSubProbeResultsWithTextIn(p.rep.Timeline, index, want)
}

// inboundTextAfter reports whether inbound assistant or result text after index
// contains want. Outbound lines never match.
func (p *claudeSubProbeProcess) inboundTextAfter(index int, want string) bool {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return claudeSubProbeInboundTextIn(p.rep.Timeline, index, want)
}

func (p *claudeSubProbeProcess) outboundInterruptAfter(index int) int {
	return p.rec.outboundInterruptSince(index)
}

// claudeSubProbeValidateArgv checks the production argv carries the switches
// that decide what the CLI loads, so a probe process can never run with a
// different tool or MCP configuration than a normal session. It runs inside the
// spawn seam, so a mismatch fails the start before any event arrives.
func claudeSubProbeValidateArgv(args []string) error {
	if !claudeSubProbeHasFlag(args, "--strict-mcp-config") {
		return errors.New("probe argv is missing --strict-mcp-config")
	}
	sources, ok := claudeSubProbeArgValue(args, "--setting-sources")
	if !ok {
		return errors.New("probe argv is missing --setting-sources")
	}
	if sources != "" {
		return fmt.Errorf("probe argv --setting-sources value is %q, want an empty value", sources)
	}
	if count := claudeSubProbeArgCount(args, "--tools"); count != 1 {
		return fmt.Errorf("probe argv carries %d --tools switches, want exactly 1", count)
	}
	if value, ok := claudeSubProbeArgValue(args, "--tools"); !ok || value != "" {
		return fmt.Errorf("probe argv --tools value is %q, want an empty value", value)
	}
	if count := claudeSubProbeArgCount(args, "--allowedTools"); count != 1 {
		return fmt.Errorf("probe argv carries %d --allowedTools switches, want exactly 1", count)
	}
	allowed, ok := claudeSubProbeArgValue(args, "--allowedTools")
	if !ok {
		return errors.New("probe argv is missing --allowedTools")
	}
	if want := claudeSubToolPrefix + "*"; allowed != want {
		return fmt.Errorf("probe argv --allowedTools value is %q, want %q", allowed, want)
	}
	return nil
}

// claudeSubProbeHasFlag reports whether args carries flag, bare or with a value.
func claudeSubProbeHasFlag(args []string, flag string) bool {
	return claudeSubProbeArgCount(args, flag) > 0
}

// claudeSubProbeArgCount counts the occurrences of flag, bare or with "=value".
func claudeSubProbeArgCount(args []string, flag string) int {
	count := 0
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			count++
		}
	}
	return count
}

// claudeSubProbeArgValue returns the value of flag, either the next argv entry or
// the part after "=".
func claudeSubProbeArgValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"="), true
		}
	}
	return "", false
}
