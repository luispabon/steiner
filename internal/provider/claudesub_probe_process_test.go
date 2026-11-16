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

// Step budgets of one probe experiment. Their sum stays inside the experiment's
// own bound, so a stalled step reports its own reason instead of the experiment
// deadline.
const (
	claudeSubProbeInitTimeout   = 20 * time.Second
	claudeSubProbeReadyTimeout  = 20 * time.Second
	claudeSubProbeResultTimeout = 20 * time.Second
	claudeSubProbeTailWindow    = 3 * time.Second
	claudeSubProbeHandlerPoll   = 25 * time.Millisecond
)

// claudeSubProbeCallState is one MCP host call as the probe observed it, with the
// outcome that distinguishes a call still waiting from one a result claimed or a
// cancellation retired.
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
// spawn seam is wrapped, to record the exact argv, environment shape, argv
// switches and every exchanged line. The returned process has passed the init
// gate, so it is the only point from which a prompt may be written.
func claudeSubProbeStartProcess(ctx context.Context, cfg claudeSubProbeConfig, rep *claudeSubProbeProcessReport, key string, spec claudeSubStartSpec) (*claudeSubProbeProcess, error) {
	rec := newClaudeSubProbeRecorder(rep, cfg.Model)
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{
		WorkDir:            cfg.WorkDir,
		ToolOutputMaxBytes: claudeSubProbeToolOutputMaxBytes,
	})
	pool.spawn = func(ctx context.Context, path string, args, env []string, dir string) (claudeSubConn, error) {
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
	if err := claudeSubProbeAwaitInit(ctx, process, claudeSubProbeInitTimeout); err != nil {
		if closeErr := process.teardown(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close probe process after a failed init gate: %w", closeErr))
		}
		return nil, err
	}
	return process, nil
}

// claudeSubProbeAwaitInit waits for the process's init event and reports the
// validation result. A missing, late or invalid init ends the experiment before
// any prompt is written.
func claudeSubProbeAwaitInit(ctx context.Context, p *claudeSubProbeProcess, timeout time.Duration) error {
	if _, err := p.pumpUntil(ctx, timeout, "init event", claudeSubProbeIsInit); err != nil {
		return fmt.Errorf("probe init gate: %w", err)
	}
	if err := p.rec.initGate(); err != nil {
		return fmt.Errorf("probe init gate: %w", err)
	}
	return nil
}

// teardown closes the process through the production retire path, records the
// exit evidence, and stops the pool. It runs once and returns the session's
// teardown result.
func (p *claudeSubProbeProcess) teardown() error {
	p.teardownOnce.Do(func() {
		if p.sess == nil {
			return
		}
		exit := &claudeSubProbeExit{}
		p.pool.retireAndRelease(p.key, p.sess)
		exit.ProcErr = claudeSubProbeErrorString(p.sess.procErr)
		exit.ConnErr = claudeSubProbeErrorString(p.sess.conn.Err())
		if p.tee != nil {
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

// sendPrompt writes one synthetic user turn through the production writer. It
// refuses until the process's init event has been validated.
func (p *claudeSubProbeProcess) sendPrompt(prompt string) error {
	if err := p.rec.initGate(); err != nil {
		return fmt.Errorf("send probe prompt: %w", err)
	}
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
// timeout is reported as the step's own reason, distinct from the experiment
// deadline.
func (p *claudeSubProbeProcess) pumpUntil(ctx context.Context, timeout time.Duration, what string, stop func(claudeSubProbeEntry) bool) (claudeSubProbeEntry, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	entry, ok, err := p.rec.pump(waitCtx, stop)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
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

// sampleMCP records the MCP host's call state at one labelled moment. resolved
// reports whether the probe has already resolved a call, which is what tells a
// claimed result apart from a cancellation.
func (p *claudeSubProbeProcess) sampleMCP(label string, resolved bool) {
	p.rep.MCPCalls = append(p.rep.MCPCalls, claudeSubProbeCallSample{
		AtMS:  p.rec.at(),
		Label: label,
		Calls: claudeSubProbeHostCalls(p.sess.host, resolved),
	})
}

// hostCallState reports the state of one call id.
func (p *claudeSubProbeProcess) hostCallState(id string, resolved bool) claudeSubProbeCallState {
	for _, state := range claudeSubProbeHostCalls(p.sess.host, resolved) {
		if state.ID == id {
			return state
		}
	}
	if p.sess.host == nil {
		return claudeSubProbeCallState{ID: id}
	}
	closed, retired := claudeSubProbeHostFlags(p.sess.host, id)
	state := claudeSubProbeCallState{ID: id, HostClosed: closed, Retired: retired}
	state.Outcome = claudeSubProbeCallOutcome(state, resolved)
	return state
}

// claudeSubProbeCallOutcome names what happened to one call.
func claudeSubProbeCallOutcome(state claudeSubProbeCallState, resolved bool) string {
	switch {
	case state.Present && state.Waiting:
		return "waiting"
	case state.Present && state.HasResult:
		return "result stored, handler not yet released"
	case state.HostClosed:
		return "closed by host teardown"
	case resolved:
		return "claimed (the probe's late result was consumed)"
	case state.Retired:
		return "cancelled (retired without a result)"
	default:
		return "absent"
	}
}

// claudeSubProbeHostCalls snapshots the live MCP calls of host.
func claudeSubProbeHostCalls(host *claudeSubMCPHost, resolved bool) []claudeSubProbeCallState {
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
		state.Outcome = claudeSubProbeCallOutcome(state, resolved)
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
		if state := p.hostCallState(id, false); state.Owner && state.Waiting {
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

// claudeSubProbeResultsAfter returns the results recorded at or after index.
func (p *claudeSubProbeProcess) resultsAfter(index int) []claudeSubProbeEntry {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return claudeSubProbeResultsIn(p.rep.Timeline, index)
}

// claudeSubProbeResultsWithTextAfter returns the results after index whose text
// names want.
func (p *claudeSubProbeProcess) resultsWithTextAfter(index int, want string) []claudeSubProbeEntry {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return claudeSubProbeResultsWithTextIn(p.rep.Timeline, index, want)
}

// claudeSubProbeInboundTextAfter reports whether inbound assistant or result
// text after index contains want. Outbound lines never match.
func (p *claudeSubProbeProcess) inboundTextAfter(index int, want string) bool {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return claudeSubProbeInboundTextIn(p.rep.Timeline, index, want)
}

func (p *claudeSubProbeProcess) outboundInterruptAfter(index int) int {
	return p.rec.outboundInterruptSince(index)
}
