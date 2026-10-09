package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// claudeSubConn is a running claude CLI process driven over stream-json stdin
// and stdout. Send and Close are safe for concurrent use, and Send never blocks
// on the child's stdin pipe.
type claudeSubConn interface {
	// Send enqueues line plus a trailing newline for the CLI's stdin and returns
	// without waiting for the write to complete. It reports an error only when
	// the connection is already closed or line exceeds the stdin size ceiling.
	Send(line []byte) error
	// Events yields decoded stdout events and is closed when the process exits.
	Events() <-chan claudeSubEvent
	// Err reports why the process exited. It is nil while the process is still
	// running and after a clean exit; later code reads it once Events is closed
	// to surface the exit cause and stderr tail. The plan's Send/Events/Close
	// shape alone cannot carry the exit cause, so Err is the minimal addition.
	Err() error
	// Close is idempotent. It starts the single lifecycle coordinator on the
	// first call and then waits for the shared terminal cleanup result or ctx,
	// whichever comes first. A caller's ctx never controls another caller: an
	// expired ctx returns a non-nil error while the coordinator keeps running,
	// so a later caller still observes the terminal result. It returns nil only
	// once the whole process tree was terminated and the child reaped without a
	// terminal cleanup error.
	Close(ctx context.Context) error
	// ForceClose is Close with forced teardown: the coordinator skips its grace
	// intervals, so terminal cleanup fits the caller's deadline instead of
	// spending two grace periods past it. Discovery uses it so its reserved
	// teardown budget is never overrun. Like Close it waits on the shared
	// terminal result or ctx, and one caller's ctx never controls another.
	ForceClose(ctx context.Context) error
	// RequestShutdown starts the single lifecycle coordinator without waiting
	// for it. It is the non-blocking fatal handoff: a control writer whose Send
	// failed calls it so a dead control channel can never leave a live CLI. The
	// owner still joins the terminal result through Close or ForceClose.
	RequestShutdown(cause error)
}

// claudeSubEvent is one decoded stdout envelope. Type and Subtype are the
// top-level "type" and "subtype" fields; Raw is the full original line.
type claudeSubEvent struct {
	Type    string
	Subtype string
	Raw     json.RawMessage
}

// claudeSubSpawner starts a claude CLI process. ctx bounds spawn setup only:
// the process outlives the request context, so implementations must not use
// exec.CommandContext.
type claudeSubSpawner func(ctx context.Context, path string, args, env []string, dir string) (claudeSubConn, error)

type claudeSubSessionKeyContext struct{}

func claudeSubContextWithSessionKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, claudeSubSessionKeyContext{}, key)
}

func claudeSubSessionKeyFromContext(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(claudeSubSessionKeyContext{}).(string)
	return key, ok && key != ""
}

// claudeSubStdoutRecorder appends raw nonempty stdout lines to one per-session
// JSONL file. It is only created when STEINER_CLAUDESUB_RECORD is non-empty.
type claudeSubStdoutRecorder struct {
	mu        sync.Mutex
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

func newClaudeSubStdoutRecorder(root, sessionKey string) (*claudeSubStdoutRecorder, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create claude_subscription recording directory: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect claude_subscription recording directory: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("claude_subscription recording path is not a directory")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure claude_subscription recording directory: %w", err)
	}

	digest := sha256.Sum256([]byte(sessionKey))
	path := filepath.Join(root, fmt.Sprintf("%x.jsonl", digest))
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("claude_subscription recording file is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect claude_subscription recording file: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open claude_subscription recording file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure claude_subscription recording file: %w", err)
	}
	return &claudeSubStdoutRecorder{file: file}, nil
}

func (r *claudeSubStdoutRecorder) writeLine(line []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return errors.New("claude_subscription stdout recorder is closed")
	}
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	n, err := r.file.Write(buf)
	if err == nil && n != len(buf) {
		err = io.ErrShortWrite
	}
	return err
}

func (r *claudeSubStdoutRecorder) close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closeErr = r.file.Close()
		r.file = nil
		r.mu.Unlock()
	})
	return r.closeErr
}

// claudeSubControlTimeout bounds how long a control request waits for its
// response. Tests shorten it.
var claudeSubControlTimeout = 30 * time.Second

type claudeSubControlResult struct {
	response json.RawMessage
	err      error
}

// claudeSubControl correlates control_request lines with their control_response
// replies over one connection. dispatch runs on the router goroutine while
// request and its helpers run on the caller's goroutine. Outbound lines are
// queued and written by one goroutine, so neither the caller nor the router
// blocks on a stalled connection write.
type claudeSubControl struct {
	conn claudeSubConn

	mu       sync.Mutex
	pending  map[string]chan claudeSubControlResult
	seq      atomic.Uint64
	closed   bool
	closeErr error

	sendMu     sync.Mutex
	sendCond   *sync.Cond
	sendQueue  [][]byte
	sendClosed bool
	writerDone chan struct{}

	fatalOnce sync.Once
}

func newClaudeSubControl(conn claudeSubConn) *claudeSubControl {
	c := &claudeSubControl{
		conn:       conn,
		pending:    make(map[string]chan claudeSubControlResult),
		writerDone: make(chan struct{}),
	}
	c.sendCond = sync.NewCond(&c.sendMu)
	go c.writeLoop()
	return c
}

// enqueue queues an outbound control line without blocking.
func (c *claudeSubControl) enqueue(line []byte) {
	c.sendMu.Lock()
	if c.sendClosed {
		c.sendMu.Unlock()
		return
	}
	c.sendQueue = append(c.sendQueue, line)
	c.sendMu.Unlock()
	c.sendCond.Signal()
}

func (c *claudeSubControl) writeLoop() {
	defer close(c.writerDone)
	for {
		c.sendMu.Lock()
		for len(c.sendQueue) == 0 && !c.sendClosed {
			c.sendCond.Wait()
		}
		if len(c.sendQueue) == 0 {
			c.sendMu.Unlock()
			return
		}
		line := c.sendQueue[0]
		c.sendQueue = c.sendQueue[1:]
		c.sendMu.Unlock()

		if err := c.conn.Send(line); err != nil {
			// A send failure is terminal for the whole control, exactly like the
			// router seeing Events close, and it hands off to the transport so a
			// dead control channel cannot leave a live CLI.
			c.fatal(fmt.Errorf("send control line: %w", err))
			return
		}
	}
}

// terminate atomically enters the terminal state: it records the cause, marks
// the control closed, fails every pending request and stops the outbound queue.
// It is safe to call from any goroutine, including the writer.
func (c *claudeSubControl) terminate(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	err := cause
	if err == nil {
		err = c.conn.Err()
	}
	if err == nil {
		err = errors.New("claude CLI connection closed")
	}
	c.closeErr = err
	for id, ch := range c.pending {
		delete(c.pending, id)
		select {
		case ch <- claudeSubControlResult{err: err}:
		default:
		}
	}
	c.mu.Unlock()

	c.sendMu.Lock()
	c.sendClosed = true
	c.sendQueue = nil
	c.sendCond.Broadcast()
	c.sendMu.Unlock()
}

// close terminates the control and joins its writer. It must not be called from
// the writer goroutine.
func (c *claudeSubControl) close() {
	c.terminate(nil)
	<-c.writerDone
}

// fatal enters the terminal state and, once, hands off to the transport's
// non-blocking shutdown so a control channel that can no longer write cannot
// leave the CLI alive. It is safe to call from any goroutine and never blocks.
func (c *claudeSubControl) fatal(cause error) {
	c.terminate(cause)
	c.fatalOnce.Do(func() { c.conn.RequestShutdown(cause) })
}

// dispatch delivers a control message and reports whether ev was consumed.
// Non-control events return false so the router can forward them. CLI-originated
// control_request messages are answered with an "unsupported" error because
// steiner registers no hooks or SDK MCP servers; the response is queued, so the
// router never blocks on the send.
func (c *claudeSubControl) dispatch(ev claudeSubEvent) bool {
	switch ev.Type {
	case "control_response":
		var envelope struct {
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
				Error     string          `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal(ev.Raw, &envelope); err != nil {
			return true
		}
		res := envelope.Response
		var result claudeSubControlResult
		switch res.Subtype {
		case "success":
			result.response = res.Response
		case "error":
			result.err = errors.New(res.Error)
		default:
			result.err = fmt.Errorf("claude CLI returned unknown control response subtype %q", res.Subtype)
		}

		c.mu.Lock()
		ch := c.pending[res.RequestID]
		c.mu.Unlock()
		if ch == nil {
			return true // unknown or already answered request id
		}
		select {
		case ch <- result:
		default: // duplicate response; keep the router non-blocking
		}
		return true
	case "control_request":
		var envelope struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(ev.Raw, &envelope)
		line, _ := json.Marshal(map[string]any{ // cannot fail: only string values
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "error",
				"request_id": envelope.RequestID,
				"error":      "unsupported",
			},
		})
		c.enqueue(line)
		return true
	}
	return false
}

// request queues one control request and waits for its response, ctx
// cancellation, the control timeout, or the connection closing.
func (c *claudeSubControl) request(ctx context.Context, subtype string, fields map[string]any) (json.RawMessage, error) {
	id := fmt.Sprintf("steiner-%d", c.seq.Add(1))
	request := map[string]any{"subtype": subtype}
	for k, v := range fields {
		request[k] = v
	}
	line, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": id,
		"request":    request,
	})
	if err != nil {
		return nil, fmt.Errorf("encode %s control request: %w", subtype, err)
	}

	ch := make(chan claudeSubControlResult, 1)
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, err
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	c.enqueue(line)

	timer := time.NewTimer(claudeSubControlTimeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("claude CLI %s: %w", subtype, res.err)
		}
		return res.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("claude CLI %s: no response within %s", subtype, claudeSubControlTimeout)
	}
}

func (c *claudeSubControl) interrupt(ctx context.Context) (json.RawMessage, error) {
	return c.request(ctx, "interrupt", nil)
}

func (c *claudeSubControl) getUsage(ctx context.Context) (json.RawMessage, error) {
	return c.request(ctx, "get_usage", map[string]any{"skip_behaviors": true})
}

func (c *claudeSubControl) setModel(ctx context.Context, model string) (json.RawMessage, error) {
	return c.request(ctx, "set_model", map[string]any{"model": model})
}

func (c *claudeSubControl) setEffort(ctx context.Context, effort string) (json.RawMessage, error) {
	return c.request(ctx, "apply_flag_settings", map[string]any{"settings": map[string]any{"effortLevel": effort}})
}

// claudeSubQueue is an unbounded, cancellation-aware FIFO of connection events.
type claudeSubQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []claudeSubEvent
	closed bool
}

func newClaudeSubQueue() *claudeSubQueue {
	q := &claudeSubQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends ev without blocking. Events pushed after close are dropped.
func (q *claudeSubQueue) push(ev claudeSubEvent) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, ev)
	q.cond.Signal()
}

// pop returns the oldest event, waiting until one is available, the queue
// closes or ctx is done. ok is false in the latter two cases.
func (q *claudeSubQueue) pop(ctx context.Context) (claudeSubEvent, bool) {
	ev, ok, _ := q.popInterruptible(ctx, nil)
	return ev, ok
}

// popInterruptible waits for the oldest event. An interrupt is checked before
// removing an ordinary event, including when both are already ready. The third
// result reports that the wait ended because interrupt was signaled.
func (q *claudeSubQueue) popInterruptible(ctx context.Context, interrupt <-chan struct{}) (claudeSubEvent, bool, bool) {
	wake := make(chan struct{})
	defer close(wake)
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-interrupt:
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-wake:
		}
	}()

	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if interrupt != nil {
			select {
			case <-interrupt:
				return claudeSubEvent{}, false, true
			default:
			}
		}
		if len(q.items) > 0 {
			ev := q.items[0]
			q.items = q.items[1:]
			return ev, true, false
		}
		if q.closed || ctx.Err() != nil {
			return claudeSubEvent{}, false, false
		}
		q.cond.Wait()
	}
}

// close marks the queue closed and wakes every waiter. Buffered events stay
// readable until drained.
func (q *claudeSubQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// drain removes and returns every buffered event.
func (q *claudeSubQueue) drain() []claudeSubEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

// claudeSubRoute pumps events through control.dispatch, forwarding non-control
// events to q when q is non-nil. It returns when events closes or stop is
// closed, and then releases any pending control requests and joins the control
// writer. A nil stop means only the events channel can end the router. Because
// the queue is unbounded, push never blocks and dispatch only queues sends, the
// router never blocks, so control responses are always delivered even while no
// caller reads the queue.
func claudeSubRoute(events <-chan claudeSubEvent, stop <-chan struct{}, control *claudeSubControl, q *claudeSubQueue, observers ...func(claudeSubEvent)) {
	defer func() {
		if control != nil {
			control.close()
		}
		if q != nil {
			q.close()
		}
	}()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if control != nil && control.dispatch(ev) {
				continue
			}
			for _, observer := range observers {
				if observer != nil {
					observer(ev)
				}
			}
			if q != nil {
				q.push(ev)
			}
		case <-stop:
			return
		}
	}
}

// writeClaudeSubUser sends one steiner turn to the CLI: a user message carrying
// blocks (text and/or tool results).
func writeClaudeSubUser(conn claudeSubConn, blocks []map[string]any) error {
	line, err := json.Marshal(map[string]any{
		"type":               "user",
		"session_id":         "",
		"message":            map[string]any{"role": "user", "content": blocks},
		"parent_tool_use_id": nil,
	})
	if err != nil {
		return fmt.Errorf("encode user message: %w", err)
	}
	return conn.Send(line)
}
