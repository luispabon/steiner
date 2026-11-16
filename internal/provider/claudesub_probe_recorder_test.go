//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"sync"
	"time"
	"unicode/utf8"
)

// Bounds of the probe's recorder: how many timeline entries one process keeps,
// how far observed events may queue ahead of the probe, and the excerpt caps for
// synthetic text.
const (
	claudeSubProbeObserveBuffer  = 8192
	claudeSubProbeTextMaxRunes   = 500
	claudeSubProbeAnswerMaxRunes = 4000
)

// claudeSubProbeTimelineMax is the per-process timeline cap. It is a variable so
// the harness self-check can lower it.
var claudeSubProbeTimelineMax = 6000

// errClaudeSubProbeEnded reports that the process's recorded exchange ended
// before the probe observed what it was waiting for.
var errClaudeSubProbeEnded = errors.New("probe process exchange ended")

// claudeSubProbeUsage is one usage snapshot's four counters.
type claudeSubProbeUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
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
	Index       int    `json:"index"`
	Prompt      string `json:"prompt"`
	Interrupted bool   `json:"interrupted,omitempty"`
	Events      int    `json:"events"`
	// AnswerText is the turn's assistant text, kept up to
	// claudeSubProbeAnswerMaxRunes without any inserted marker. AnswerTruncated
	// records that more arrived and was dropped.
	AnswerText      string                `json:"answer_text,omitempty"`
	AnswerTruncated bool                  `json:"answer_truncated,omitempty"`
	Result          *claudeSubProbeResult `json:"result,omitempty"`
	AssistantUsage  *claudeSubProbeUsage  `json:"assistant_usage,omitempty"`
	StreamUsage     *claudeSubProbeUsage  `json:"stream_usage,omitempty"`
	// LastAssistantUUID is the uuid of the turn's last assistant echo, which is
	// the resume target a later process restores.
	LastAssistantUUID string   `json:"last_assistant_uuid,omitempty"`
	AssistantUUIDs    []string `json:"assistant_uuids,omitempty"`
	// SawDelta records that streamed text deltas supplied the answer, so the
	// assistant echo is not appended a second time.
	SawDelta bool `json:"-"`
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

// claudeSubProbeObserved is one inbound event handed to the probe's live
// consumer after it was recorded.
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

	mu        sync.Mutex
	report    *claudeSubProbeProcessReport
	current   *claudeSubProbeTurn
	fatal     error
	initValid bool
	dropped   int
	truncated bool
	closed    bool

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
	if claudeSubProbeIsInit(entry) {
		init := claudeSubProbeParseInit(ev, entry.AtMS)
		r.report.Init = init
		if init.SessionID != "" {
			r.report.SessionID = init.SessionID
		}
		if err := claudeSubProbeValidateInit(init, r.model); err != nil {
			if r.fatal == nil {
				r.fatal = err
			}
		} else {
			r.initValid = true
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
		if entry.UUID != "" {
			turn.LastAssistantUUID = entry.UUID
			if len(turn.AssistantUUIDs) < 100 {
				turn.AssistantUUIDs = append(turn.AssistantUUIDs, entry.UUID)
			}
		}
		if entry.Text != "" && !turn.SawDelta {
			turn.AnswerText, turn.AnswerTruncated = claudeSubProbeAppendAnswer(turn.AnswerText, turn.AnswerTruncated, entry.Text)
		}
	case entry.StreamEvent == "message_start" || entry.StreamEvent == "message_delta":
		if entry.Usage != nil {
			turn.StreamUsage = entry.Usage
		}
	}
	if entry.DeltaType == "text_delta" {
		turn.SawDelta = true
		if entry.Text != "" {
			turn.AnswerText, turn.AnswerTruncated = claudeSubProbeAppendAnswer(turn.AnswerText, turn.AnswerTruncated, entry.Text)
		}
	}
}

// claudeSubProbeAppendAnswer appends assistant text to current, keeping at most
// claudeSubProbeAnswerMaxRunes runes. It never inserts a marker into the answer;
// truncated reports that text was dropped.
func claudeSubProbeAppendAnswer(current string, truncated bool, add string) (string, bool) {
	if add == "" {
		return current, truncated
	}
	have := utf8.RuneCountInString(current)
	if have >= claudeSubProbeAnswerMaxRunes {
		return current, true
	}
	room := claudeSubProbeAnswerMaxRunes - have
	runes := []rune(add)
	if len(runes) > room {
		return current + string(runes[:room]), true
	}
	return current + add, truncated
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

// appendLocked appends entry to the timeline, bounded by the entry cap. A hit
// cap is recorded so the report can say the timeline is incomplete.
func (r *claudeSubProbeRecorder) appendLocked(entry claudeSubProbeEntry) {
	if len(r.report.Timeline) >= claudeSubProbeTimelineMax {
		r.truncated = true
		r.report.TimelineTruncated = true
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

// timelineLen reports how many entries the timeline holds.
func (r *claudeSubProbeRecorder) timelineLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.report.Timeline)
}

// outboundInterruptSince reports the index of the first outbound interrupt
// control request at or after index, or -1.
func (r *claudeSubProbeRecorder) outboundInterruptSince(index int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return claudeSubProbeOutboundInterruptIn(r.report.Timeline, index)
}

// fatalErr reports the init validation failure, if any.
func (r *claudeSubProbeRecorder) fatalErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

// initGate reports whether the process may receive a prompt: false until a valid
// init event has been recorded, and the validation error when one failed.
func (r *claudeSubProbeRecorder) initGate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fatal != nil {
		return r.fatal
	}
	if !r.initValid {
		return errors.New("probe init event was not validated for this process")
	}
	return nil
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

// drainPending counts the observed events still queued for the probe. They are
// already in the timeline, so this only reports how many the probe never read.
func (r *claudeSubProbeRecorder) drainPending() int {
	count := 0
	for {
		select {
		case <-r.obs:
			count++
		default:
			return count
		}
	}
}

// close stops observation. It is safe to call more than once.
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
