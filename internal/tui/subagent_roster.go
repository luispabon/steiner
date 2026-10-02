package tui

import (
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
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
	agentID    string
	agentType  string
	occurrence occurrenceKey // the occurrence the row currently shows
	admitted   bool
	group      string
	status     string
	startTime  int64 // unix nano; set on queue, reset when the agent starts
	finishTime int64 // unix nano; 0 until finished
	delivered  bool
	seq        int // insertion order, tie-breaker for deterministic sorting
}

func (e rosterEntry) finished() bool {
	return e.status == rosterDone || e.status == rosterFailed || e.status == rosterLost
}

// ownedElsewhere reports whether a live admitted occurrence other than key
// owns the row, so events for key must leave it alone.
func (e rosterEntry) ownedElsewhere(key occurrenceKey) bool {
	return e.admitted && !e.finished() && e.occurrence != key
}

// subAgentRoster tracks sub-agents for the sidebar and status bar. It is fed
// from Model event handling and deliberately does not read contentBuffer.
// Rows are per agent so follow-ups reuse their agent's row; each row tracks
// the occurrence it currently shows.
type subAgentRoster struct {
	entries    map[string]*rosterEntry
	admissions map[occurrenceKey]output.DelegationAdmission
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

// admission returns the accepted admission of key when it names agentID.
func (r *subAgentRoster) admission(agentID string, key occurrenceKey) (output.DelegationAdmission, bool) {
	admission, ok := r.admissions[key]
	return admission, ok && admission.AgentID == agentID
}

func (r *subAgentRoster) begin(occ output.DelegationOccurrence, agentType, status string, now int64) {
	agentID := strings.TrimSpace(occ.AgentID)
	if !validRosterAgent(agentID, agentType) {
		return
	}
	key := keyOf(occ)
	admission, admitted := r.admission(agentID, key)
	e, existed := r.entries[agentID]
	if existed && !admitted && e.ownedElsewhere(key) {
		return
	}
	if !existed {
		e = r.upsert(agentID)
	}
	restarting := existed && (e.finished() || e.occurrence != key)
	if restarting {
		e.clearRun()
	}
	e.bind(key, admission, admitted)
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

func (e *rosterEntry) clearRun() {
	e.status = ""
	e.startTime = 0
	e.finishTime = 0
	e.delivered = false
}

func (e *rosterEntry) bind(key occurrenceKey, admission output.DelegationAdmission, admitted bool) {
	e.occurrence = key
	e.admitted = admitted
	e.group = ""
	if admitted {
		e.group = admission.Group
	}
}

func validRosterAgent(agentID, agentType string) bool {
	return agentID != "" && !strings.EqualFold(strings.TrimSpace(agentType), "advisor")
}

func (r *subAgentRoster) finish(occ output.DelegationOccurrence, agentType, status string, durationMs, now int64) {
	agentID := strings.TrimSpace(occ.AgentID)
	if !validRosterAgent(agentID, agentType) {
		return
	}
	e, ok := r.finishEntry(agentID, keyOf(occ))
	if !ok {
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

// finishEntry resolves the row a terminal event for key settles. A row showing
// another occurrence is taken over only by an admitted key and only once no
// live admitted occurrence owns it.
func (r *subAgentRoster) finishEntry(agentID string, key occurrenceKey) (*rosterEntry, bool) {
	admission, admitted := r.admission(agentID, key)
	e, exists := r.entries[agentID]
	switch {
	case !exists:
		if !admitted {
			return nil, false
		}
		e = r.upsert(agentID)
		e.bind(key, admission, admitted)
	case e.occurrence != key:
		if !admitted || e.ownedElsewhere(key) {
			return nil, false
		}
		e.clearRun()
		e.bind(key, admission, true)
	}
	return e, true
}

// observe updates the roster from one output event. Sub-agent scoped tool
// events are ignored: sub-agents cannot nest, so only parent calls carry groups.
func (r *subAgentRoster) observe(event output.Event, now int64) {
	switch p := event.Payload.(type) {
	case output.DelegationAcceptedEvent:
		r.admit(p.CallID, output.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, AgentID: p.AgentID, BatchID: p.BatchID, Group: p.Group})
	case output.ToolCallFinishedEvent:
		if p.DelegationAdmission != nil && p.DelegationAdmission.Status == tool.DelegationAdmissionAccepted {
			r.admit(p.CallID, *p.DelegationAdmission)
		}
	case output.DelegationQueuedEvent:
		r.begin(p.DelegationOccurrence, p.AgentType, rosterQueued, now)
	case output.DelegationStartedEvent:
		r.begin(p.DelegationOccurrence, p.AgentType, rosterRunning, now)
	case output.DelegationCompleteEvent:
		r.finish(p.DelegationOccurrence, p.AgentType, completionStatus(p.Status), p.DurationMs, now)
	case output.DelegationFailedEvent:
		r.finish(p.DelegationOccurrence, p.AgentType, rosterFailed, p.DurationMs, now)
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

// admit records an accepted admission and applies it to the agent's row when
// the row already shows that occurrence.
func (r *subAgentRoster) admit(callID string, admission output.DelegationAdmission) {
	if callID == "" || admission.AgentID == "" {
		return
	}
	if r.admissions == nil {
		r.admissions = map[occurrenceKey]output.DelegationAdmission{}
	}
	key := occurrenceKey{BatchID: admission.BatchID, CallID: callID}
	r.admissions[key] = admission
	if e, ok := r.entries[admission.AgentID]; ok && e.occurrence == key {
		e.bind(key, admission, true)
	}
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
	if !validRosterAgent(id, item.AgentType) {
		return
	}
	e, ok := r.deliveryEntry(id, occurrenceKey{BatchID: item.BatchID, CallID: item.ParentCallID})
	if !ok {
		return
	}
	if !e.finished() || item.Status == rosterLost {
		if e.startTime == 0 {
			e.startTime = now - item.DurationMs*1_000_000
		}
		e.status = deliveredRosterStatus(item.Status)
		e.finishTime = e.startTime + item.DurationMs*1_000_000
	}
	e.delivered = true
}

// deliveryEntry resolves the row a delivered item settles. An item without a
// parent call ID addresses the agent's row; a row that has not yet shown any
// occurrence adopts the item's.
func (r *subAgentRoster) deliveryEntry(agentID string, key occurrenceKey) (*rosterEntry, bool) {
	admission, admitted := r.admission(agentID, key)
	e, exists := r.entries[agentID]
	switch {
	case !exists:
		e = r.upsert(agentID)
		e.bind(key, admission, admitted)
	case key.CallID == "" || e.occurrence == key:
	case e.occurrence.CallID == "" && !e.admitted:
		e.bind(key, admission, admitted)
	case !admitted || e.ownedElsewhere(key):
		return nil, false
	default:
		e.clearRun()
		e.bind(key, admission, true)
	}
	return e, true
}

func deliveredRosterStatus(status string) string {
	switch status {
	case rosterLost:
		return rosterLost
	case "failed", "error":
		return rosterFailed
	default:
		return rosterDone
	}
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
