package tui

import (
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// Roster statuses for a sub-agent entry.
const (
	rosterQueued  = "queued"
	rosterRunning = "running"
	rosterDone    = "done"
	rosterFailed  = "failed"
	rosterLost    = "lost"
)

// rosterEntry is one sub-agent dispatched since the last user prompt. It is
// comparable so snapshots can be diffed for render caching.
type rosterEntry struct {
	agentID       string
	currentCallID string
	agentType     string
	group         string
	batchID       string
	accepted      bool
	status        string
	startTime     int64 // unix nano; set on queue, reset when the agent starts
	finishTime    int64 // unix nano; 0 until finished
	delivered     bool
	seq           int // insertion order, tie-breaker for deterministic sorting
}

func (e rosterEntry) finished() bool {
	return e.status == rosterDone || e.status == rosterFailed || e.status == rosterLost
}

// subAgentRoster tracks sub-agents for the sidebar and status bar. It is fed
// from Model event handling and deliberately does not read contentBuffer.
type subAgentRoster struct {
	entries    map[string]*rosterEntry
	admissions map[string]output.DelegationAdmission // keyed by current parent call ID
	nextSeq    int
}

func (r *subAgentRoster) upsert(agentID string) *rosterEntry {
	if r.entries == nil {
		r.entries = map[string]*rosterEntry{}
	}
	e, ok := r.entries[agentID]
	if !ok {
		e = &rosterEntry{agentID: agentID, seq: r.nextSeq}
		r.nextSeq++
		r.entries[agentID] = e
	}
	return e
}

func (r *subAgentRoster) begin(agentID, agentType, callID, status string, now int64) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || strings.EqualFold(strings.TrimSpace(agentType), "advisor") {
		return
	}
	admission, accepted := r.admissions[callID]
	if accepted && admission.AgentID != agentID {
		accepted = false
	}
	e, existed := r.entries[agentID]
	if existed && e.accepted && !e.finished() && !accepted && (callID == "" || e.currentCallID != callID) {
		return
	}
	if !existed {
		e = r.upsert(agentID)
	}
	restarting := e.finished() || (e.currentCallID != "" && e.currentCallID != callID)
	if restarting {
		e.status = status
		e.startTime = now
		e.finishTime = 0
		e.delivered = false
	}
	e.currentCallID = callID
	e.accepted = accepted
	e.group = ""
	e.batchID = ""
	if accepted {
		e.group = admission.Group
		e.batchID = admission.BatchID
	}
	if !restarting {
		if t := strings.TrimSpace(agentType); t != "" {
			e.agentType = t
		}
	}
	if e.startTime == 0 || status == rosterRunning {
		e.startTime = now
	}
	e.status = status
}

func (r *subAgentRoster) finish(agentID, agentType, callID, status string, durationMs, now int64) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || strings.EqualFold(strings.TrimSpace(agentType), "advisor") {
		return
	}
	e, ok := r.entries[agentID]
	if callID != "" {
		admission, accepted := r.admissions[callID]
		if !accepted || admission.AgentID != agentID {
			return
		}
		if ok && e.currentCallID != callID {
			return
		}
		if !ok {
			e = r.upsert(agentID)
			e.currentCallID = callID
			e.accepted = true
			e.group = admission.Group
			e.batchID = admission.BatchID
		}
	} else if !ok {
		e = r.upsert(agentID)
	}
	if e.accepted && callID == "" && status != rosterDone {
		return
	}
	if t := strings.TrimSpace(agentType); t != "" && e.agentType == "" {
		e.agentType = t
	}
	if e.startTime == 0 {
		e.startTime = now - durationMs*1_000_000
	}
	if e.finished() {
		return
	}
	e.status = status
	e.finishTime = now
	if durationMs > 0 {
		e.finishTime = e.startTime + durationMs*1_000_000
	}
}

// observe updates the roster from one output event. Sub-agent scoped tool
// events are ignored: sub-agents cannot nest, so only parent calls carry groups.
func (r *subAgentRoster) observe(event output.Event, now int64) {
	switch p := event.Payload.(type) {
	case output.DelegationAcceptedEvent:
		admission := output.DelegationAdmission{Status: "accepted", AgentID: p.AgentID, BatchID: p.BatchID, Group: p.Group}
		r.recordAdmission(p.CallID, admission)
		r.applyAdmission(p.CallID, admission)
	case output.ToolCallFinishedEvent:
		if p.DelegationAdmission != nil && p.DelegationAdmission.Status == "accepted" {
			r.recordAdmission(p.CallID, *p.DelegationAdmission)
			r.applyAdmission(p.CallID, *p.DelegationAdmission)
		}
	case output.DelegationQueuedEvent:
		r.begin(p.AgentID, p.AgentType, p.CallID, rosterQueued, now)
	case output.DelegationStartedEvent:
		r.begin(p.AgentID, p.AgentType, p.CallID, rosterRunning, now)
	case output.DelegationCompleteEvent:
		r.finish(p.AgentID, p.AgentType, "", completionStatus(p.Status), p.DurationMs, now)
	case output.DelegationFailedEvent:
		r.finish(p.AgentID, p.AgentType, p.CallID, rosterFailed, p.DurationMs, now)
	case output.SubAgentsDeliveredEvent:
		for _, item := range p.Items {
			r.deliver(item, now)
		}
	case output.ContextDiagnosticsEvent:
		if p.Kind == "session_loaded" {
			r.prune()
		}
	case output.ContextBudgetEvent:
		if output.ContextDiagnosticKind(p) == "session_loaded" {
			r.prune()
		}
	}
}

// applyAdmission updates identity only when an earlier lifecycle event has already
// established the same current call and agent.
func (r *subAgentRoster) applyAdmission(callID string, admission output.DelegationAdmission) {
	if callID == "" || admission.AgentID == "" {
		return
	}
	if e, ok := r.entries[admission.AgentID]; ok && e.currentCallID == callID {
		e.accepted = true
		e.group = admission.Group
		e.batchID = admission.BatchID
	}
}

func (r *subAgentRoster) recordAdmission(callID string, admission output.DelegationAdmission) {
	if callID == "" || admission.AgentID == "" {
		return
	}
	if r.admissions == nil {
		r.admissions = map[string]output.DelegationAdmission{}
	}
	r.admissions[callID] = admission
}

// completionStatus maps a delegation completion status to a roster status.
func completionStatus(status string) string {
	if status == "failed" || status == "error" {
		return rosterFailed
	}
	return rosterDone
}

func (r *subAgentRoster) deliver(item output.DeliveredSubAgent, now int64) {
	id := strings.TrimSpace(item.AgentID)
	if id == "" || strings.EqualFold(strings.TrimSpace(item.AgentType), "advisor") {
		return
	}
	admission, accepted := r.admissions[item.ParentCallID]
	if accepted && admission.AgentID != id {
		accepted = false
	}
	e, known := r.entries[id]
	if !known {
		e = r.upsert(id)
	}
	currentMatch := item.ParentCallID == "" || e.currentCallID == "" || e.currentCallID == item.ParentCallID
	if accepted && currentMatch && (!e.accepted || e.currentCallID == item.ParentCallID) {
		e.currentCallID = item.ParentCallID
		e.accepted = true
		e.group = admission.Group
		e.batchID = admission.BatchID
	}
	if !accepted && currentMatch && !e.accepted {
		e.group = ""
		e.batchID = ""
	}
	if !e.finished() || item.Status == rosterLost {
		if e.accepted && !currentMatch {
			return
		}
		status := rosterDone
		switch item.Status {
		case rosterLost:
			status = rosterLost
		case "failed", "error":
			status = rosterFailed
		}
		if e.startTime == 0 {
			e.startTime = now - item.DurationMs*1_000_000
		}
		e.status = status
		e.finishTime = e.startTime + item.DurationMs*1_000_000
	}
	e.delivered = true
}

// prune drops every finished entry; running and queued ones stay. It is used
// when replayed history is discarded.
func (r *subAgentRoster) prune() {
	r.dropWhere(func(e *rosterEntry) bool { return e.finished() })
}

// pruneDelivered drops finished entries whose result has been delivered. A
// finished entry still awaiting delivery stays, so it keeps its group when the
// delivery arrives.
func (r *subAgentRoster) pruneDelivered() {
	r.dropWhere(func(e *rosterEntry) bool { return e.finished() && e.delivered })
}

func (r *subAgentRoster) dropWhere(drop func(*rosterEntry) bool) {
	for id, e := range r.entries {
		if drop(e) {
			delete(r.entries, id)
		}
	}
	if len(r.entries) == 0 {
		r.admissions = nil
	}
}

// snapshot returns entries sorted by start time (then insertion order).
func (r *subAgentRoster) snapshot() []rosterEntry {
	out := make([]rosterEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].startTime != out[j].startTime {
			return out[i].startTime < out[j].startTime
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// rosterCounts summarises a snapshot.
type rosterCounts struct {
	total, queued, running, finished, failed int
}

func countRoster(entries []rosterEntry) rosterCounts {
	c := rosterCounts{total: len(entries)}
	for _, e := range entries {
		switch e.status {
		case rosterQueued:
			c.queued++
		case rosterRunning:
			c.running++
		default:
			c.finished++
			if e.status != rosterDone {
				c.failed++
			}
		}
	}
	return c
}

func (r *subAgentRoster) hasRunning() bool {
	for _, e := range r.entries {
		if e.status == rosterRunning {
			return true
		}
	}
	return false
}

// observeRoster feeds one event into the roster.
func (m *Model) observeRoster(event output.Event) {
	m.roster.observe(event, nanoNow())
}

// pruneRosterOnPrompt clears delivered, finished entries when the user submits
// a prompt.
func (m *Model) pruneRosterOnPrompt() {
	m.roster.pruneDelivered()
	m.syncRoster()
}

// syncRoster pushes the roster snapshot into the sidebar and status bar. The
// spinner frame only advances while a sub-agent is running.
func (m *Model) syncRoster() {
	snap := m.roster.snapshot()
	c := countRoster(snap)
	m.sidebar.subAgents = snap
	m.sidebar.subAgentsNow = nanoNow() / 1_000_000_000 * 1_000_000_000
	m.status.subAgentsTotal = c.total
	m.status.subAgentsFinished = c.finished
	m.status.subAgentsQueued = c.queued
	m.status.subAgentsFailed = c.failed
	m.status.spinnerFrame = 0
	if c.running > 0 {
		m.status.spinnerFrame = m.sidebar.tickCount % len(spinnerFrames)
	}
}
