//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

	// claudeSubProbeInitWorstCase is the worst case of the init gate: no init
	// before the first input, a warmup turn that spends its whole result budget,
	// then the post-warmup init budget.
	claudeSubProbeInitWorstCase = claudeSubProbeInitBeforeInput + claudeSubProbeWarmupResult + claudeSubProbeInitAfterWarmup

	claudeSubProbeWarmupPrompt        = "PROBE-WARMUP: reply with the single word ok and do not call any tool."
	claudeSubProbeInitPathBeforeInput = "init before input"
	claudeSubProbeInitPathWarmup      = "init after warmup"
)

// claudeSubProbeCallHost is the MCP host subset the warmup rejection uses. The
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
func claudeSubProbeStartProcess(ctx context.Context, cfg claudeSubProbeConfig, rep *claudeSubProbeProcessReport, key string, spec claudeSubStartSpec) (*claudeSubProbeProcess, error) {
	rec := newClaudeSubProbeRecorder(rep, cfg.Model)
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{
		WorkDir:            cfg.WorkDir,
		ToolOutputMaxBytes: claudeSubProbeToolOutputMaxBytes,
	})
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
		return newClaudeSubProbeTeeConn(conn, rec), nil
	}
	sess, err := pool.acquire(ctx, key, spec)
	if err != nil {
		rec.close()
		// acquire tore down any partially started session itself, so the pool holds
		// no session here; closing it only stops the reaper goroutine.
		_ = pool.Close()
		return nil, err
	}
	tee, _ := sess.conn.(*claudeSubProbeTeeConn)
	process := &claudeSubProbeProcess{key: key, pool: pool, sess: sess, tee: tee, rec: rec, rep: rep}
	if err := claudeSubProbeGateInit(ctx, process, claudeSubProbeInitBeforeInput, claudeSubProbeInitAfterWarmup, claudeSubProbeWarmupResult); err != nil {
		if closeErr := process.teardown(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close probe process after a failed init gate: %w", closeErr))
		}
		return nil, err
	}
	return process, nil
}

// claudeSubProbeGateInit settles the process's init event. When init arrives
// before input it is validated directly. Otherwise the probe sends one fixed
// warmup turn, because the CLI may only report init after the first user input,
// and holds every other prompt until init validates. While the warmup turn runs,
// any MCP tool call is resolved with an error result, so no tool work can happen
// before the probe knows what the process loaded.
func claudeSubProbeGateInit(ctx context.Context, p *claudeSubProbeProcess, beforeInput, afterWarmup, warmupResult time.Duration) error {
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

	p.rep.InitPath = claudeSubProbeInitPathWarmup
	p.rec.beginTurn(claudeSubProbeWarmupPrompt)
	if err := p.sendRawPrompt(claudeSubProbeWarmupPrompt); err != nil {
		return err
	}
	rejectCtx, stopReject := context.WithCancel(ctx)
	rejectDone := make(chan struct{})
	go func() {
		defer close(rejectDone)
		claudeSubProbeRejectMCPCalls(rejectCtx, p.hostSnapshot(), p.sess.host, func(id string) {
			p.rep.WarmupRejectedCalls = append(p.rep.WarmupRejectedCalls, id)
		})
	}()

	var sawResult bool
	_, err = p.pumpUntil(ctx, afterWarmup, "init event after the warmup turn", func(entry claudeSubProbeEntry) bool {
		if claudeSubProbeIsResult(entry) {
			sawResult = true
		}
		return claudeSubProbeIsInit(entry)
	})
	stopReject()
	<-rejectDone
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
		if _, err := p.pumpUntil(ctx, warmupResult, "warmup result", claudeSubProbeIsResult); err != nil {
			return fmt.Errorf("probe init gate: %w", err)
		}
	}
	return nil
}

// claudeSubProbeRejectMCPCalls resolves every live MCP call with an error result
// until ctx ends. It keeps a warmup turn from doing tool work before init is
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
			host.resolve(call, claudeSubToolResult{Text: "probe warmup turn rejected this tool call: init is not validated", IsError: true})
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

// hostSnapshot returns a function reporting the host's live call states.
func (p *claudeSubProbeProcess) hostSnapshot() func() []claudeSubProbeCallState {
	if p.sess == nil || p.sess.host == nil {
		return nil
	}
	return func() []claudeSubProbeCallState { return claudeSubProbeHostCalls(p.sess.host) }
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
			exit.Status, exit.WaitErr = claudeSubProbeExitStatus(p.tee.claudeSubConn)
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
// It is meaningful only after teardown reaped the child.
func claudeSubProbeExitStatus(conn claudeSubConn) (int, string) {
	proc, ok := conn.(*claudeSubProcess)
	if !ok {
		return 0, ""
	}
	proc.errMu.Lock()
	waitErr := proc.waitErr
	proc.errMu.Unlock()
	if waitErr == nil {
		return 0, ""
	}
	return claudeSubExitCode(waitErr), claudeSubProbeErrorString(waitErr)
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
// Only the warmup turn, which exists to make the CLI report init, uses it.
func (p *claudeSubProbeProcess) sendRawPrompt(prompt string) error {
	blocks := claudeSubUserBlocks([]Message{{Role: MessageRoleUser, Content: prompt}})
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

func (p *claudeSubProbeProcess) runTurn(ctx context.Context, prompt string) error {
	if _, err := p.beginTurn(prompt); err != nil {
		return err
	}
	return p.awaitResult(ctx)
}

// awaitResult waits for a terminal result.
func (p *claudeSubProbeProcess) awaitResult(ctx context.Context) error {
	_, err := p.pumpUntil(ctx, claudeSubProbeResultTimeout, "result", claudeSubProbeIsResult)
	return err
}

// awaitInterruptedResult waits for the result of a turn the probe interrupted.
func (p *claudeSubProbeProcess) awaitInterruptedResult(ctx context.Context) error {
	_, err := p.pumpUntil(ctx, claudeSubProbeResultTimeout, "result after interrupt", claudeSubProbeIsResult)
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
// time left, and as the experiment deadline once the parent context expired.
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
	if !claudeSubProbeHasFlag(args, "--setting-sources") {
		return errors.New("probe argv is missing --setting-sources")
	}
	value, ok := claudeSubProbeArgValue(args, "--tools")
	if !ok {
		return errors.New("probe argv is missing --tools")
	}
	if value != "" {
		return fmt.Errorf("probe argv --tools value is %q, want an empty value", value)
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
	for _, arg := range args {
		if arg == flag || len(arg) > len(flag) && arg[:len(flag)+1] == flag+"=" {
			return true
		}
	}
	return false
}

// claudeSubProbeArgValue returns the value of flag, either the next argv entry or
// the part after "=".
func claudeSubProbeArgValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if len(arg) > len(flag) && arg[:len(flag)+1] == flag+"=" {
			return arg[len(flag)+1:], true
		}
	}
	return "", false
}
