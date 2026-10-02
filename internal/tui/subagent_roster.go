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
type rosterAdmissionIdentity struct {
	callID  string
	batchID string
}

type subAgentRoster struct {
	entries          map[string]*rosterEntry
	admissions       map[string]output.DelegationAdmission // keyed by current parent call ID
	latestAdmissions map[string]rosterAdmissionIdentity    // keyed by agent ID
	nextSeq          int
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
	if !validRosterAgent(agentID, agentType) {
		return
	}
	admission, accepted := r.admissionForAgent(callID, agentID)
	e, existed := r.entries[agentID]
	if r.rosterBeginRejected(e, existed, agentID, callID, admission, accepted) {
		return
	}
	if !existed {
		e = r.upsert(agentID)
	}
	restarting := e.finished() || (e.currentCallID != "" && e.currentCallID != callID) || (accepted && e.accepted && e.batchID != admission.BatchID)
	e.applyBegin(agentType, callID, status, now, admission, accepted, restarting)
}

// rosterBeginRejected reports whether an existing roster entry blocks a begin
// with the given admission identity.
func (r *subAgentRoster) rosterBeginRejected(e *rosterEntry, existed bool, agentID, callID string, admission output.DelegationAdmission, accepted bool) bool {
	if !existed {
		return false
	}
	if accepted && !r.latestAdmissionMatches(agentID, callID, admission) {
		return true
	}
	return rosterEntryRejectsBegin(e, callID, admission, accepted)
}

// applyBegin writes the begin state onto the roster entry, resetting on a
// restart and otherwise carrying the previous agent type forward.
func (e *rosterEntry) applyBegin(agentType, callID, status string, now int64, admission output.DelegationAdmission, accepted, restarting bool) {
	if restarting {
		e.clearRun()
	}
	e.setCall(callID, admission, accepted)
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

func (e *rosterEntry) setCall(callID string, admission output.DelegationAdmission, accepted bool) {
	e.currentCallID = callID
	e.accepted = accepted
	e.group, e.batchID = admissionIdentity(admission, accepted)
}

func validRosterAgent(agentID, agentType string) bool {
	return agentID != "" && !strings.EqualFold(strings.TrimSpace(agentType), "advisor")
}

func (r *subAgentRoster) admissionForAgent(callID, agentID string) (output.DelegationAdmission, bool) {
	admission, ok := r.admissions[callID]
	return admission, ok && admission.AgentID == agentID
}

func admissionIdentity(admission output.DelegationAdmission, accepted bool) (group, batch string) {
	if accepted {
		return admission.Group, admission.BatchID
	}
	return "", ""
}

func rosterEntryRejectsBegin(e *rosterEntry, callID string, admission output.DelegationAdmission, accepted bool) bool {
	if !e.accepted || e.finished() {
		return false
	}
	if !accepted {
		return callID == "" || e.currentCallID != callID
	}
	return e.currentCallID == callID && e.batchID != admission.BatchID
}

func rosterEntryMatchesAdmission(e *rosterEntry, callID string, admission output.DelegationAdmission) bool {
	return e.accepted && e.currentCallID == callID && e.batchID == admission.BatchID
}

func (r *subAgentRoster) latestAdmissionMatches(agentID, callID string, admission output.DelegationAdmission) bool {
	latest, ok := r.latestAdmissions[agentID]
	return ok && latest == (rosterAdmissionIdentity{callID: callID, batchID: admission.BatchID})
}

func (r *subAgentRoster) deliveryAdmissionMatchesCurrent(e *rosterEntry, callID, agentID string, admission output.DelegationAdmission, accepted bool) bool {
	if !accepted || !r.latestAdmissionMatches(agentID, callID, admission) {
		return false
	}
	return !e.accepted || rosterEntryMatchesAdmission(e, callID, admission)
}

func (r *subAgentRoster) finish(agentID, agentType, callID, status string, durationMs, now int64) {
	agentID = strings.TrimSpace(agentID)
	if !validRosterAgent(agentID, agentType) {
		return
	}
	e, ok := r.finishEntry(agentID, callID)
	if !ok || (e.accepted && callID == "" && status != rosterDone) {
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

func (r *subAgentRoster) finishEntry(agentID, callID string) (*rosterEntry, bool) {
	e, exists := r.entries[agentID]
	if callID != "" {
		admission, accepted := r.admissionForAgent(callID, agentID)
		if !accepted || !r.latestAdmissionMatches(agentID, callID, admission) {
			return nil, false
		}
		if !exists {
			e = r.upsert(agentID)
			e.setCall(callID, admission, true)
		} else if !rosterEntryMatchesAdmission(e, callID, admission) {
			if !e.finished() {
				return nil, false
			}
			e.clearRun()
			e.setCall(callID, admission, true)
		}
	} else if !exists {
		e = r.upsert(agentID)
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

// admit records the admission, then updates identity only when an earlier
// lifecycle event has already established the same current call and agent.
func (r *subAgentRoster) admit(callID string, admission output.DelegationAdmission) {
	if callID == "" || admission.AgentID == "" {
		return
	}
	if r.admissions == nil {
		r.admissions = map[string]output.DelegationAdmission{}
	}
	if r.latestAdmissions == nil {
		r.latestAdmissions = map[string]rosterAdmissionIdentity{}
	}
	r.admissions[callID] = admission
	r.latestAdmissions[admission.AgentID] = rosterAdmissionIdentity{callID: callID, batchID: admission.BatchID}
	e, ok := r.entries[admission.AgentID]
	if !ok || e.currentCallID != callID || (e.accepted && e.batchID != admission.BatchID) {
		return
	}
	e.accepted = true
	e.group = admission.Group
	e.batchID = admission.BatchID
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
	admission, accepted := r.admissionForAgent(item.ParentCallID, id)
	e, known := r.entries[id]
	if !known {
		e = r.upsert(id)
	}
	currentMatch := rosterDeliveryMatchesCurrentCall(e, item.ParentCallID)
	admissionMatch := r.deliveryAdmissionMatchesCurrent(e, item.ParentCallID, id, admission, accepted)
	applyRosterDeliveryIdentity(e, item.ParentCallID, admission, admissionMatch, currentMatch)
	if !r.applyRosterDeliveryStatus(e, item, now, currentMatch) {
		return
	}
	e.delivered = true
}

func rosterDeliveryMatchesCurrentCall(e *rosterEntry, callID string) bool {
	return callID == "" || e.currentCallID == "" || e.currentCallID == callID
}

func applyRosterDeliveryIdentity(e *rosterEntry, callID string, admission output.DelegationAdmission, accepted, currentMatch bool) {
	if accepted && currentMatch && (!e.accepted || e.currentCallID == callID) {
		e.currentCallID = callID
		e.accepted = true
		e.group, e.batchID = admissionIdentity(admission, true)
	}
	if !accepted && currentMatch && !e.accepted {
		e.group, e.batchID = "", ""
	}
}

func (r *subAgentRoster) applyRosterDeliveryStatus(e *rosterEntry, item output.DeliveredSubAgent, now int64, currentMatch bool) bool {
	if e.finished() && item.Status != rosterLost {
		return true
	}
	if e.accepted && !currentMatch {
		return false
	}
	status := deliveredRosterStatus(item.Status)
	if e.startTime == 0 {
		e.startTime = now - item.DurationMs*1_000_000
	}
	e.status = status
	e.finishTime = e.startTime + item.DurationMs*1_000_000
	return true
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
			if r.latestAdmissions[e.agentID] == (rosterAdmissionIdentity{callID: e.currentCallID, batchID: e.batchID}) {
				delete(r.latestAdmissions, e.agentID)
			}
		}
	}
	if len(r.entries) == 0 {
		r.admissions = nil
		r.latestAdmissions = nil
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
