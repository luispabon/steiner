//go:build claudesub_probe

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Bounds of the live probe: how much of one process's exchange the report keeps,
// how far inbound events may queue ahead of the probe, the text excerpt cap, and
// the waits that end a stalled experiment instead of hanging it.
const (
	claudeSubProbeTimelineMax     = 6000
	claudeSubProbeObserveBuffer   = 8192
	claudeSubProbeTextMaxRunes    = 500
	claudeSubProbeResultTimeout   = 20 * time.Second
	claudeSubProbeTailWindow      = 3 * time.Second
	claudeSubProbeReadyTimeout    = 30 * time.Second
	claudeSubProbeTeardownTimeout = 60 * time.Second
	claudeSubProbeHandlerPoll     = 25 * time.Millisecond
)

// errClaudeSubProbeEnded reports that the process's recorded exchange ended
// before the probe observed what it was waiting for.
var errClaudeSubProbeEnded = errors.New("probe process exchange ended")

// claudeSubProbeOwnEnvKeys are the child environment variables whose values the
// report may state. Every other value is redacted.
var claudeSubProbeOwnEnvKeys = []string{
	"CLAUDE_CODE_RESUME_INTERRUPTED_TURN",
	"CLAUDE_CODE_RESUME_SHOWS_CUT_OFF_CALLS",
	"CLAUDE_CODE_RESUME_PROMPT",
	"CLAUDE_CONFIG_DIR",
	"HOME",
}

// claudeSubProbeEnv is the reportable shape of the child environment: every key,
// the values of the probe-relevant non-secret keys, and nothing else.
type claudeSubProbeEnv struct {
	Keys   []string          `json:"keys"`
	Values map[string]string `json:"non_secret_values"`
}

// claudeSubProbeUsage is one usage snapshot's four counters.
type claudeSubProbeUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

// claudeSubProbeInit is what the CLI's init event reports about the process.
type claudeSubProbeInit struct {
	AtMS           int64           `json:"at_ms"`
	SessionID      string          `json:"session_id,omitempty"`
	Model          string          `json:"model,omitempty"`
	Tools          []string        `json:"tools"`
	Capabilities   json.RawMessage `json:"capabilities,omitempty"`
	CLIVersion     string          `json:"cli_version,omitempty"`
	PermissionMode string          `json:"permission_mode,omitempty"`
}

// claudeSubProbeResult is one terminal result event, reduced to the fields the
// probe measures.
type claudeSubProbeResult struct {
	AtMS           int64                `json:"at_ms"`
	Subtype        string               `json:"subtype,omitempty"`
	TerminalReason string               `json:"terminal_reason,omitempty"`
	IsError        bool                 `json:"is_error"`
	StopReason     string               `json:"stop_reason,omitempty"`
	Text           string               `json:"text,omitempty"`
	Usage          *claudeSubProbeUsage `json:"usage,omitempty"`
}

// claudeSubProbeTurn is one probe-authored turn: the synthetic prompt it sent
// and what the CLI did with it.
type claudeSubProbeTurn struct {
	Index          int                   `json:"index"`
	Prompt         string                `json:"prompt"`
	Interrupted    bool                  `json:"interrupted,omitempty"`
	Events         int                   `json:"events"`
	Result         *claudeSubProbeResult `json:"result,omitempty"`
	AssistantUsage *claudeSubProbeUsage  `json:"assistant_usage,omitempty"`
	StreamUsage    *claudeSubProbeUsage  `json:"stream_usage,omitempty"`
	AnswerText     string                `json:"answer_text,omitempty"`
	AssistantUUIDs []string              `json:"assistant_uuids,omitempty"`
}

// claudeSubProbeEntry is one recorded line of the exchange. Inbound entries
// carry the fields the CLI reported; outbound entries carry what the probe
// wrote, with only the synthetic text kept.
type claudeSubProbeEntry struct {
	AtMS             int64                `json:"at_ms"`
	Dir              string               `json:"dir"`
	Type             string               `json:"type,omitempty"`
	Subtype          string               `json:"subtype,omitempty"`
	TerminalReason   string               `json:"terminal_reason,omitempty"`
	StopReason       string               `json:"stop_reason,omitempty"`
	IsError          *bool                `json:"is_error,omitempty"`
	UUID             string               `json:"uuid,omitempty"`
	SessionID        string               `json:"session_id,omitempty"`
	ParentToolUseID  *string              `json:"parent_tool_use_id,omitempty"`
	RequestID        string               `json:"request_id,omitempty"`
	ControlSubtype   string               `json:"control_subtype,omitempty"`
	ResponseSubtype  string               `json:"response_subtype,omitempty"`
	ResponseBody     string               `json:"response_body,omitempty"`
	StreamEvent      string               `json:"stream_event,omitempty"`
	DeltaType        string               `json:"delta_type,omitempty"`
	ContentBlockType string               `json:"content_block_type,omitempty"`
	ToolUseID        string               `json:"tool_use_id,omitempty"`
	Status           string               `json:"status,omitempty"`
	Usage            *claudeSubProbeUsage `json:"usage,omitempty"`
	Text             string               `json:"text,omitempty"`
}

// claudeSubProbeCallState is one MCP host call as the probe observed it.
type claudeSubProbeCallState struct {
	ID         string `json:"id"`
	Registered bool   `json:"registered"`
	Owner      bool   `json:"owner"`
	Waiting    bool   `json:"waiting"`
	HasResult  bool   `json:"has_result"`
	Done       bool   `json:"done"`
}

// claudeSubProbeCallSample is the host call state at one labelled moment.
type claudeSubProbeCallSample struct {
	AtMS  int64                     `json:"at_ms"`
	Label string                    `json:"label"`
	Calls []claudeSubProbeCallState `json:"calls"`
}

// claudeSubProbeExit is the exit evidence for one process.
type claudeSubProbeExit struct {
	AtMS         int64  `json:"at_ms"`
	ProcErr      string `json:"proc_err,omitempty"`
	ConnErr      string `json:"conn_err,omitempty"`
	PoolCloseErr string `json:"pool_close_err,omitempty"`
}

// claudeSubProbeResume is the resume target a process started with.
type claudeSubProbeResume struct {
	SessionID string `json:"session_id,omitempty"`
	AtUUID    string `json:"at_uuid,omitempty"`
	Fork      bool   `json:"fork,omitempty"`
}

// claudeSubProbeProcessReport is one CLI process: the argv and environment shape
// it ran with, its whole recorded exchange, and its exit evidence.
type claudeSubProbeProcessReport struct {
	Name       string                     `json:"name"`
	Key        string                     `json:"key"`
	Persist    bool                       `json:"persist"`
	Resume     claudeSubProbeResume       `json:"resume,omitempty"`
	Dir        string                     `json:"dir,omitempty"`
	Argv       []string                   `json:"argv,omitempty"`
	Env        *claudeSubProbeEnv         `json:"env,omitempty"`
	StartError string                     `json:"start_error,omitempty"`
	Init       *claudeSubProbeInit        `json:"init,omitempty"`
	SessionID  string                     `json:"session_id,omitempty"`
	Turns      []*claudeSubProbeTurn      `json:"turns,omitempty"`
	Timeline   []claudeSubProbeEntry      `json:"timeline,omitempty"`
	Dropped    int                        `json:"dropped_events,omitempty"`
	MCPCalls   []claudeSubProbeCallSample `json:"mcp_calls,omitempty"`
	Exit       *claudeSubProbeExit        `json:"exit,omitempty"`
	Findings   []string                   `json:"findings,omitempty"`
}

// claudeSubProbeObserved is one inbound event plus its recorded entry, handed to
// the probe's live consumer after it is recorded.
type claudeSubProbeObserved struct {
	entry claudeSubProbeEntry
}

// claudeSubProbeRecorder records one process's exchanged lines in order and
// forwards each inbound event to the probe's consumer. It sits on the process
// connection, so it sees every stdout line including control responses, which
// the production router consumes before they could reach the event queue.
type claudeSubProbeRecorder struct {
	start time.Time
	model string

	mu      sync.Mutex
	report  *claudeSubProbeProcessReport
	current *claudeSubProbeTurn
	fatal   error
	dropped int
	closed  bool

	obs  chan claudeSubProbeObserved
	done chan struct{}
}

func newClaudeSubProbeRecorder(report *claudeSubProbeProcessReport, model string) *claudeSubProbeRecorder {
	return &claudeSubProbeRecorder{
		start:  time.Now(),
		model:  model,
		report: report,
		obs:    make(chan claudeSubProbeObserved, claudeSubProbeObserveBuffer),
		done:   make(chan struct{}),
	}
}

// at reports milliseconds since the process's recorder was created.
func (r *claudeSubProbeRecorder) at() int64 {
	return time.Since(r.start).Milliseconds()
}

// addInbound records one stdout event and its derived state: the init report and
// its validation, the turn it belongs to, and the session id it names.
func (r *claudeSubProbeRecorder) addInbound(ev claudeSubEvent) {
	entry := claudeSubProbeParseInbound(ev, r.at())
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.appendLocked(entry)
	if entry.SessionID != "" {
		r.report.SessionID = entry.SessionID
	}
	if entry.Type == "system" && entry.Subtype == "init" {
		init := claudeSubProbeParseInit(ev, entry.AtMS)
		r.report.Init = init
		if init.SessionID != "" {
			r.report.SessionID = init.SessionID
		}
		if err := claudeSubProbeValidateInit(init, r.model); err != nil && r.fatal == nil {
			r.fatal = err
		}
	}
	turn := r.current
	if turn != nil {
		r.recordTurnLocked(turn, entry)
		if entry.Type == "result" {
			r.current = nil
		}
	}
	r.mu.Unlock()

	select {
	case r.obs <- claudeSubProbeObserved{entry: entry}:
	default:
		r.mu.Lock()
		r.dropped++
		r.report.Dropped = r.dropped
		r.mu.Unlock()
	}
}

// recordTurnLocked folds one inbound entry into the turn it belongs to.
func (r *claudeSubProbeRecorder) recordTurnLocked(turn *claudeSubProbeTurn, entry claudeSubProbeEntry) {
	turn.Events++
	switch {
	case entry.Type == "result":
		isError := entry.IsError != nil && *entry.IsError
		turn.Result = &claudeSubProbeResult{
			AtMS:           entry.AtMS,
			Subtype:        entry.Subtype,
			TerminalReason: entry.TerminalReason,
			IsError:        isError,
			StopReason:     entry.StopReason,
			Text:           entry.Text,
			Usage:          entry.Usage,
		}
	case entry.Type == "assistant":
		if entry.Usage != nil {
			turn.AssistantUsage = entry.Usage
		}
		if entry.UUID != "" && len(turn.AssistantUUIDs) < 10 {
			turn.AssistantUUIDs = append(turn.AssistantUUIDs, entry.UUID)
		}
	case entry.StreamEvent == "message_start" || entry.StreamEvent == "message_delta":
		if entry.Usage != nil {
			turn.StreamUsage = entry.Usage
		}
	}
	if entry.Text != "" && (entry.Type == "assistant" || entry.DeltaType == "text_delta") {
		turn.AnswerText = claudeSubProbeExcerpt(turn.AnswerText + entry.Text)
	}
}

// addOutbound records one line the probe wrote to the CLI.
func (r *claudeSubProbeRecorder) addOutbound(line []byte) {
	entry := claudeSubProbeParseOutbound(line, r.at())
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.appendLocked(entry)
}

// appendLocked appends entry to the timeline, bounded by the entry cap.
func (r *claudeSubProbeRecorder) appendLocked(entry claudeSubProbeEntry) {
	if len(r.report.Timeline) >= claudeSubProbeTimelineMax {
		return
	}
	r.report.Timeline = append(r.report.Timeline, entry)
}

// beginTurn records a probe-authored turn as the current one and returns it.
func (r *claudeSubProbeRecorder) beginTurn(prompt string) *claudeSubProbeTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn := &claudeSubProbeTurn{Index: len(r.report.Turns), Prompt: claudeSubProbeExcerpt(prompt)}
	r.report.Turns = append(r.report.Turns, turn)
	r.current = turn
	return turn
}

// fatalErr reports the init validation failure, if any.
func (r *claudeSubProbeRecorder) fatalErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

// pump consumes observed events until stop matches, the exchange ends, or ctx is
// done. A nil stop consumes until ctx is done.
func (r *claudeSubProbeRecorder) pump(ctx context.Context, stop func(claudeSubProbeEntry) bool) (claudeSubProbeEntry, bool, error) {
	for {
		if err := r.fatalErr(); err != nil {
			return claudeSubProbeEntry{}, false, err
		}
		select {
		case <-ctx.Done():
			return claudeSubProbeEntry{}, false, ctx.Err()
		case <-r.done:
			return claudeSubProbeEntry{}, false, errClaudeSubProbeEnded
		case obs := <-r.obs:
			if stop != nil && stop(obs.entry) {
				return obs.entry, true, nil
			}
		}
	}
}

// close stops observation. It is safe to call once after the process's exchange
// has ended.
func (r *claudeSubProbeRecorder) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	close(r.done)
}

// claudeSubProbeTeeConn records every line on a production process connection
// before the session's router sees it, and records every line the probe writes.
// Inbound lines are recorded on the forwarding goroutine and outbound lines on
// the caller, so same-direction order is exact while the relative order of an
// inbound and an outbound line is approximate.
type claudeSubProbeTeeConn struct {
	claudeSubConn
	rec  *claudeSubProbeRecorder
	out  chan claudeSubEvent
	stop chan struct{}
	done chan struct{}
}

func newClaudeSubProbeTeeConn(conn claudeSubConn, rec *claudeSubProbeRecorder) *claudeSubProbeTeeConn {
	tee := &claudeSubProbeTeeConn{
		claudeSubConn: conn,
		rec:           rec,
		out:           make(chan claudeSubEvent),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go tee.forward()
	return tee
}

// forward copies the process's events into the session's channel, recording each
// one first.
func (c *claudeSubProbeTeeConn) forward() {
	defer close(c.done)
	defer close(c.out)
	for {
		select {
		case <-c.stop:
			return
		case ev, ok := <-c.claudeSubConn.Events():
			if !ok {
				return
			}
			c.rec.addInbound(ev)
			select {
			case c.out <- ev:
			case <-c.stop:
				return
			}
		}
	}
}

func (c *claudeSubProbeTeeConn) Events() <-chan claudeSubEvent { return c.out }

func (c *claudeSubProbeTeeConn) Send(line []byte) error {
	c.rec.addOutbound(line)
	return c.claudeSubConn.Send(line)
}

// shutdown releases the forwarding goroutine once the process is gone.
func (c *claudeSubProbeTeeConn) shutdown() {
	select {
	case <-c.done:
		return
	default:
	}
	close(c.stop)
	<-c.done
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
}

// claudeSubProbeStartProcess starts one CLI process the production way: a pool
// with the probe's private cwd, then acquire, which builds argv from
// claudeSubArgs and the child environment from claudeSubChildEnv. Only the
// spawn seam is wrapped, to record the exact argv, environment shape and every
// exchanged line.
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
		rep.Dir = dir
		rep.Env = claudeSubProbeEnvSummary(env)
		return newClaudeSubProbeTeeConn(conn, rec), nil
	}
	sess, err := pool.acquire(ctx, key, spec)
	if err != nil {
		rec.close()
		_ = pool.Close()
		return nil, err
	}
	tee, _ := sess.conn.(*claudeSubProbeTeeConn)
	return &claudeSubProbeProcess{key: key, pool: pool, sess: sess, tee: tee, rec: rec, rep: rep}, nil
}

// teardown closes the process through the production retire path, records the
// exit evidence, and stops the pool. It runs once and returns the session's
// teardown result.
func (p *claudeSubProbeProcess) teardown() error {
	var teardownErr error
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
		exit.AtMS = p.rec.at()
		p.rep.Exit = exit
		p.rec.close()
		teardownErr = p.sess.procErr
	})
	return teardownErr
}

func (p *claudeSubProbeProcess) beginTurn(ctx context.Context, prompt string) (*claudeSubProbeTurn, error) {
	turn := p.rec.beginTurn(prompt)
	if err := p.sendPrompt(prompt); err != nil {
		return nil, err
	}
	return turn, nil
}

// sendPrompt writes one synthetic user turn through the production writer.
func (p *claudeSubProbeProcess) sendPrompt(prompt string) error {
	blocks := claudeSubUserBlocks([]Message{{Role: MessageRoleUser, Content: prompt}})
	if err := writeClaudeSubUser(p.sess.conn, blocks); err != nil {
		return fmt.Errorf("send probe prompt: %w", err)
	}
	return nil
}

func (p *claudeSubProbeProcess) runTurn(ctx context.Context, prompt string) error {
	if _, err := p.beginTurn(ctx, prompt); err != nil {
		return err
	}
	return p.awaitResult(ctx)
}

func (p *claudeSubProbeProcess) awaitResult(ctx context.Context) error {
	_, err := p.pumpUntil(ctx, claudeSubProbeResultTimeout, claudeSubProbeIsResult)
	return err
}

func (p *claudeSubProbeProcess) interrupt(ctx context.Context) error {
	if _, err := p.sess.control.interrupt(ctx); err != nil {
		return fmt.Errorf("send probe interrupt: %w", err)
	}
	return nil
}

// pumpUntil waits up to timeout for an event the stop predicate matches.
func (p *claudeSubProbeProcess) pumpUntil(ctx context.Context, timeout time.Duration, stop func(claudeSubProbeEntry) bool) (claudeSubProbeEntry, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	entry, ok, err := p.rec.pump(waitCtx, stop)
	if err != nil {
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
	_, _, _ = p.rec.pump(tailCtx, nil) // the deadline ends the window
}

// sampleMCP records the MCP host's call state at one labelled moment.
func (p *claudeSubProbeProcess) sampleMCP(label string) {
	p.rep.MCPCalls = append(p.rep.MCPCalls, claudeSubProbeCallSample{
		AtMS:  p.rec.at(),
		Label: label,
		Calls: claudeSubProbeHostCalls(p.sess.host),
	})
}

func (p *claudeSubProbeProcess) timelineLen() int {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return len(p.rep.Timeline)
}

func (p *claudeSubProbeProcess) turn(index int) *claudeSubProbeTurn {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	if index < 0 || index >= len(p.rep.Turns) {
		return nil
	}
	return p.rep.Turns[index]
}

// sessionID reports the CLI session id recorded for this process so far.
func (p *claudeSubProbeProcess) sessionID() string {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return p.rep.SessionID
}

// claudeSubProbeParseInbound reduces one stdout event to a recorded entry.
func claudeSubProbeParseInbound(ev claudeSubEvent, at int64) claudeSubProbeEntry {
	entry := claudeSubProbeEntry{AtMS: at, Dir: "in", Type: ev.Type, Subtype: ev.Subtype, UUID: ev.UUID, SessionID: ev.SessionID}
	var payload struct {
		ParentToolUseID *string              `json:"parent_tool_use_id"`
		TerminalReason  string               `json:"terminal_reason"`
		StopReason      string               `json:"stop_reason"`
		IsError         *bool                `json:"is_error"`
		Result          string               `json:"result"`
		Status          string               `json:"status"`
		Usage           *claudeSubProbeUsage `json:"usage"`
		RequestID       string               `json:"request_id"`
		Request         *struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Response *struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
			Error     string          `json:"error"`
		} `json:"response"`
		Event *struct {
			Type  string `json:"type"`
			Delta *struct {
				Type string `json:"type"`
			} `json:"delta"`
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content_block"`
			Message *struct {
				Usage *claudeSubProbeUsage `json:"usage"`
			} `json:"message"`
		} `json:"event"`
		Message *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				ID   string `json:"id"`
			} `json:"content"`
			Usage *claudeSubProbeUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(ev.Raw, &payload); err != nil {
		return entry
	}
	entry.ParentToolUseID = payload.ParentToolUseID
	entry.TerminalReason = payload.TerminalReason
	entry.StopReason = payload.StopReason
	entry.IsError = payload.IsError
	entry.Status = payload.Status
	entry.RequestID = payload.RequestID
	entry.Usage = payload.Usage
	if payload.Request != nil {
		entry.ControlSubtype = payload.Request.Subtype
	}
	if payload.Response != nil {
		entry.ResponseSubtype = payload.Response.Subtype
		if entry.RequestID == "" {
			entry.RequestID = payload.Response.RequestID
		}
		body := string(payload.Response.Response)
		if payload.Response.Error != "" {
			body = payload.Response.Error
		}
		entry.ResponseBody = claudeSubProbeExcerpt(body)
	}
	if payload.Event != nil {
		entry.StreamEvent = payload.Event.Type
		if payload.Event.Delta != nil {
			entry.DeltaType = payload.Event.Delta.Type
		}
		if payload.Event.ContentBlock != nil {
			entry.ContentBlockType = payload.Event.ContentBlock.Type
			if payload.Event.ContentBlock.Type == "tool_use" {
				entry.ToolUseID = payload.Event.ContentBlock.ID
			}
		}
		if payload.Event.Message != nil && payload.Event.Message.Usage != nil {
			entry.Usage = payload.Event.Message.Usage
		}
	}
	if payload.Message != nil {
		if payload.Message.Usage != nil {
			entry.Usage = payload.Message.Usage
		}
		var text strings.Builder
		for _, block := range payload.Message.Content {
			if block.Type == "tool_use" && entry.ToolUseID == "" {
				entry.ToolUseID = block.ID
			}
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		entry.Text = claudeSubProbeExcerpt(text.String())
	}
	if entry.Type == "result" && payload.Result != "" {
		entry.Text = claudeSubProbeExcerpt(payload.Result)
	}
	return entry
}

// claudeSubProbeParseOutbound reduces one line the probe wrote to a recorded
// entry. Only synthetic text is kept.
func claudeSubProbeParseOutbound(line []byte, at int64) claudeSubProbeEntry {
	entry := claudeSubProbeEntry{AtMS: at, Dir: "out"}
	var payload struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   *struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Message *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return entry
	}
	entry.Type = payload.Type
	entry.RequestID = payload.RequestID
	if payload.Request != nil {
		entry.ControlSubtype = payload.Request.Subtype
	}
	if payload.Message != nil {
		var text strings.Builder
		for _, block := range payload.Message.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		entry.Text = claudeSubProbeExcerpt(text.String())
	}
	return entry
}

// claudeSubProbeParseInit extracts the init event's reported process shape.
func claudeSubProbeParseInit(ev claudeSubEvent, at int64) *claudeSubProbeInit {
	var payload struct {
		SessionID      string          `json:"session_id"`
		Model          string          `json:"model"`
		Tools          []string        `json:"tools"`
		Capabilities   json.RawMessage `json:"capabilities"`
		CLIVersion     string          `json:"claude_code_version"`
		Version        string          `json:"version"`
		PermissionMode string          `json:"permissionMode"`
	}
	init := &claudeSubProbeInit{AtMS: at, SessionID: ev.SessionID}
	if err := json.Unmarshal(ev.Raw, &payload); err != nil {
		return init
	}
	init.Model = payload.Model
	init.Tools = payload.Tools
	init.Capabilities = payload.Capabilities
	init.CLIVersion = payload.CLIVersion
	if init.CLIVersion == "" {
		init.CLIVersion = payload.Version
	}
	init.PermissionMode = payload.PermissionMode
	if init.SessionID == "" {
		init.SessionID = payload.SessionID
	}
	return init
}

// claudeSubProbeValidateInit enforces the probe's precondition: exactly the
// synthetic probe tool and the requested model.
func claudeSubProbeValidateInit(init *claudeSubProbeInit, model string) error {
	if init.Model == "" {
		return errors.New("probe init event reported no model")
	}
	if !claudeSubProbeModelMatches(model, init.Model) {
		return fmt.Errorf("probe init model %q does not match the requested model %q", init.Model, model)
	}
	want := claudeSubToolPrefix + claudeSubProbeToolName
	if len(init.Tools) != 1 || init.Tools[0] != want {
		return fmt.Errorf("probe init tools are %v, want exactly [%s]", init.Tools, want)
	}
	return nil
}

// claudeSubProbeModelMatches accepts the requested model, its resolved id, and a
// CLI alias that resolves to an id containing the alias.
func claudeSubProbeModelMatches(requested, reported string) bool {
	if requested == "" {
		return false
	}
	if strings.EqualFold(requested, reported) {
		return true
	}
	return strings.Contains(strings.ToLower(reported), strings.ToLower(requested))
}

// claudeSubProbeHostCalls snapshots the live MCP calls of host.
func claudeSubProbeHostCalls(host *claudeSubMCPHost) []claudeSubProbeCallState {
	if host == nil {
		return nil
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	out := make([]claudeSubProbeCallState, 0, len(host.calls))
	for id, call := range host.calls {
		out = append(out, claudeSubProbeCallState{
			ID:         id,
			Registered: call.registered,
			Owner:      call.owner,
			Waiting:    call.waiter != nil,
			HasResult:  call.result != nil,
			Done:       call.done,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// claudeSubProbeAwaitMCPHandler waits until a tools/call handler owns and blocks
// on the call for id, which is window B of the interrupt experiments.
func claudeSubProbeAwaitMCPHandler(ctx context.Context, p *claudeSubProbeProcess, id string, timeout time.Duration) error {
	if p.sess.host == nil {
		return errors.New("probe session has no MCP host")
	}
	deadline := time.Now().Add(timeout)
	for {
		for _, call := range claudeSubProbeHostCalls(p.sess.host) {
			if call.ID == id && call.Owner && call.Waiting {
				return nil
			}
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

// claudeSubProbeEnvSummary reports the child environment's shape: every key and
// the values of the probe-relevant non-secret keys only.
func claudeSubProbeEnvSummary(env []string) *claudeSubProbeEnv {
	summary := &claudeSubProbeEnv{Values: map[string]string{}}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		summary.Keys = append(summary.Keys, key)
		for _, open := range claudeSubProbeOwnEnvKeys {
			if key == open {
				summary.Values[key] = value
			}
		}
	}
	sort.Strings(summary.Keys)
	for _, open := range claudeSubProbeOwnEnvKeys {
		if _, ok := summary.Values[open]; !ok {
			summary.Values[open] = "(unset)"
		}
	}
	return summary
}

// claudeSubProbeIsResult matches a terminal result event.
func claudeSubProbeIsResult(entry claudeSubProbeEntry) bool {
	return entry.Dir == "in" && entry.Type == "result"
}

// claudeSubProbeIsTextDelta matches the first streamed text of a message.
func claudeSubProbeIsTextDelta(entry claudeSubProbeEntry) bool {
	return entry.Dir == "in" && entry.DeltaType == "text_delta"
}

// claudeSubProbeIsToolUseStart matches a streamed tool_use content block start,
// which is window A of the interrupt experiments.
func claudeSubProbeIsToolUseStart(entry claudeSubProbeEntry) bool {
	if entry.Dir != "in" {
		return false
	}
	if entry.ContentBlockType == "tool_use" && entry.ToolUseID != "" {
		return true
	}
	return entry.Type == "assistant" && entry.ToolUseID != ""
}

// claudeSubProbeExcerpt cuts s to the probe's text cap at a rune boundary.
func claudeSubProbeExcerpt(s string) string {
	if utf8.RuneCountInString(s) <= claudeSubProbeTextMaxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:claudeSubProbeTextMaxRunes]) + "..."
}

func claudeSubProbeErrorString(err error) string {
	if err == nil {
		return ""
	}
	return claudeSubProbeExcerpt(err.Error())
}

func claudeSubProbeUsageString(u *claudeSubProbeUsage) string {
	if u == nil {
		return "none"
	}
	return fmt.Sprintf("input=%d cache_creation=%d cache_read=%d output=%d", u.InputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens, u.OutputTokens)
}

// claudeSubProbeConfigDir is the CLI's config root: CLAUDE_CONFIG_DIR when set,
// otherwise ~/.claude.
func claudeSubProbeConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeSubProbeProjectDir is the CLI's per-cwd session directory. The CLI
// encodes the working directory by replacing every character that is not a
// letter or digit with a dash.
func claudeSubProbeProjectDir(configDir, cwd string) string {
	encoded := make([]rune, 0, len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			encoded = append(encoded, r)
		default:
			encoded = append(encoded, '-')
		}
	}
	return filepath.Join(configDir, "projects", string(encoded))
}

// claudeSubProbeTranscriptPath is the on-disk transcript for one CLI session.
func claudeSubProbeTranscriptPath(configDir, cwd, sessionID string) string {
	if configDir == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(claudeSubProbeProjectDir(configDir, cwd), sessionID+".jsonl")
}

// claudeSubProbeTranscriptEntry is one recorded transcript line, reduced to its
// type and identifiers. Neither message text nor tool content is recorded.
type claudeSubProbeTranscriptEntry struct {
	Line    int    `json:"line"`
	Type    string `json:"type,omitempty"`
	Subtype string `json:"subtype,omitempty"`
	UUID    string `json:"uuid,omitempty"`
}

// claudeSubProbeTranscript is one snapshot of a CLI session transcript file.
type claudeSubProbeTranscript struct {
	Label   string                          `json:"label"`
	Path    string                          `json:"path"`
	Exists  bool                            `json:"exists"`
	Lines   int                             `json:"lines"`
	Entries []claudeSubProbeTranscriptEntry `json:"entries,omitempty"`
	ReadErr string                          `json:"read_error,omitempty"`
}

// claudeSubProbeReadTranscript records the shape of a session transcript: its
// line count and the type and uuid of every entry, never its content.
func claudeSubProbeReadTranscript(label, configDir, cwd, sessionID string) claudeSubProbeTranscript {
	snap := claudeSubProbeTranscript{Label: label, Path: claudeSubProbeTranscriptPath(configDir, cwd, sessionID)}
	if snap.Path == "" {
		return snap
	}
	data, err := os.ReadFile(snap.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			snap.ReadErr = claudeSubProbeErrorString(err)
		}
		return snap
	}
	snap.Exists = true
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		snap.Lines++
		var payload struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			UUID    string `json:"uuid"`
		}
		entry := claudeSubProbeTranscriptEntry{Line: i + 1}
		if json.Unmarshal([]byte(line), &payload) == nil {
			entry.Type = payload.Type
			entry.Subtype = payload.Subtype
			entry.UUID = payload.UUID
		}
		snap.Entries = append(snap.Entries, entry)
	}
	return snap
}

// claudeSubProbeFindings derives the per-process facts the report states next to
// the timeline.
func claudeSubProbeFindings(rep *claudeSubProbeProcessReport) []string {
	var out []string
	if rep.Init != nil {
		out = append(out, fmt.Sprintf("init: model=%q tools=%v cli=%q capabilities=%s", rep.Init.Model, rep.Init.Tools, rep.Init.CLIVersion, claudeSubProbeExcerpt(string(rep.Init.Capabilities))))
	}
	for i, entry := range rep.Timeline {
		if entry.Dir == "out" && entry.Type == "control_request" && entry.ControlSubtype == "interrupt" {
			out = append(out, claudeSubProbeInterruptFinding(rep.Timeline, i))
		}
	}
	for i, entry := range rep.Timeline {
		if entry.Dir == "in" && entry.Type == "result" {
			out = append(out, claudeSubProbeResultFinding(rep.Timeline, i))
		}
	}
	var cache []string
	for _, turn := range rep.Turns {
		out = append(out, claudeSubProbeTurnFinding(turn))
		cache = append(cache, fmt.Sprintf("turn%d=%d", turn.Index, claudeSubProbeCacheRead(turn)))
	}
	if len(cache) > 0 {
		out = append(out, "cache_read_input_tokens by turn: "+strings.Join(cache, " "))
	}
	return out
}

// claudeSubProbeCacheRead reports a turn's cache read tokens, preferring the
// assistant message's own usage over the query-cumulative result usage.
func claudeSubProbeCacheRead(turn *claudeSubProbeTurn) int {
	if turn.AssistantUsage != nil {
		return turn.AssistantUsage.CacheReadInputTokens
	}
	if turn.StreamUsage != nil {
		return turn.StreamUsage.CacheReadInputTokens
	}
	if turn.Result != nil && turn.Result.Usage != nil {
		return turn.Result.Usage.CacheReadInputTokens
	}
	return 0
}

func claudeSubProbeInterruptFinding(timeline []claudeSubProbeEntry, at int) string {
	sent := timeline[at]
	responseIdx, resultIdx := -1, -1
	for i := at + 1; i < len(timeline); i++ {
		entry := timeline[i]
		if responseIdx < 0 && entry.Dir == "in" && entry.Type == "control_response" && entry.RequestID == sent.RequestID {
			responseIdx = i
		}
		if resultIdx < 0 && entry.Dir == "in" && entry.Type == "result" {
			resultIdx = i
		}
	}
	describe := func(idx int) string {
		if idx < 0 {
			return "none"
		}
		return fmt.Sprintf("%dms", timeline[idx].AtMS)
	}
	order := "unknown"
	switch {
	case responseIdx < 0 && resultIdx < 0:
	case responseIdx < 0:
		order = "result first, no control_response"
	case resultIdx < 0:
		order = "control_response only, no result"
	case responseIdx < resultIdx:
		order = "control_response before result"
	default:
		order = "result before control_response"
	}
	responseSubtype := "none"
	responseBody := ""
	if responseIdx >= 0 {
		responseSubtype = timeline[responseIdx].ResponseSubtype
		responseBody = timeline[responseIdx].ResponseBody
	}
	return fmt.Sprintf("interrupt %s sent at %dms: control_response=%s subtype=%q body=%q, first result=%s, order=%s",
		sent.RequestID, sent.AtMS, describe(responseIdx), responseSubtype, responseBody, describe(resultIdx), order)
}

func claudeSubProbeResultFinding(timeline []claudeSubProbeEntry, at int) string {
	entry := timeline[at]
	isError := entry.IsError != nil && *entry.IsError
	var after []string
	for _, next := range timeline[at+1:] {
		if next.Dir != "in" {
			continue
		}
		kind := next.Type
		if next.Subtype != "" {
			kind += "/" + next.Subtype
		}
		after = append(after, kind)
		if len(after) == 10 {
			break
		}
	}
	return fmt.Sprintf("result at %dms: subtype=%q terminal_reason=%q is_error=%v stop_reason=%q usage=%s later_inbound=%v",
		entry.AtMS, entry.Subtype, entry.TerminalReason, isError, entry.StopReason, claudeSubProbeUsageString(entry.Usage), after)
}

func claudeSubProbeTurnFinding(turn *claudeSubProbeTurn) string {
	result := "none"
	if turn.Result != nil {
		result = fmt.Sprintf("subtype=%q terminal_reason=%q is_error=%v usage=%s", turn.Result.Subtype, turn.Result.TerminalReason, turn.Result.IsError, claudeSubProbeUsageString(turn.Result.Usage))
	}
	return fmt.Sprintf("turn %d interrupted=%v events=%d result=%s assistant_usage=%s answer=%q",
		turn.Index, turn.Interrupted, turn.Events, result, claudeSubProbeUsageString(turn.AssistantUsage), turn.AnswerText)
}
