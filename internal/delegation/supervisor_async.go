package delegation

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// SpawnTicket is the immediate acknowledgement of a non-blocking Spawn.
type SpawnTicket struct {
	AgentID string
	// Queued is true when the job is waiting for a running slot.
	Queued bool
}

// SetCompletionSink installs the sink that receives released completions. A nil
// sink disables posting and grouping.
func (s *Supervisor) SetCompletionSink(sink agent.CompletionSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink = sink
}

// Spawn enqueues a job without waiting for it. Its completion is posted to the
// completion sink and it stays pending until MarkDelivered acknowledges it.
func (s *Supervisor) Spawn(ctx context.Context, job ChildJob) (SpawnTicket, error) {
	state, err := s.enqueue(ctx, job, false)
	if err != nil {
		return SpawnTicket{}, err
	}
	if state.wasQueued && s.events != nil {
		s.events.Emit(output.NewDelegationQueuedEvent(job.AgentID, job.ParentCallID, string(job.AgentType), job.ObjectivePreview))
	}
	return SpawnTicket{AgentID: job.AgentID, Queued: state.wasQueued}, nil
}

// MarkDelivered acknowledges completions the parent has consumed, identified by
// their parent call ID. Only completions that are no longer held by a group are
// acknowledged; acknowledged agents leave Pending, Ledger and IsPending.
func (s *Supervisor) MarkDelivered(parentCallIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range s.jobs {
		if state.completion != nil && !state.held && slices.Contains(parentCallIDs, state.job.ParentCallID) {
			state.acked = true
		}
	}
}

// Pending returns every unacknowledged sub-agent in enqueue order.
func (s *Supervisor) Pending() []agent.PendingSubAgent {
	pending := s.pendingStates()
	out := make([]agent.PendingSubAgent, 0, len(pending))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range pending {
		out = append(out, agent.PendingSubAgent{
			AgentID:   state.job.AgentID,
			AgentType: string(state.job.AgentType),
			State:     stateOf(state),
		})
	}
	return out
}

// Ledger returns the durable record of every unacknowledged sub-agent in
// enqueue order.
func (s *Supervisor) Ledger() []agent.SubAgentLedgerEntry {
	pending := s.pendingStates()
	out := make([]agent.SubAgentLedgerEntry, 0, len(pending))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range pending {
		out = append(out, agent.SubAgentLedgerEntry{
			AgentID:      state.job.AgentID,
			AgentType:    string(state.job.AgentType),
			ParentCallID: state.job.ParentCallID,
			Group:        state.job.Group,
			WorktreePath: state.worktree.Path,
		})
	}
	return out
}

// HasPending reports whether any sub-agent is unacknowledged.
func (s *Supervisor) HasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range s.jobs {
		if !state.acked {
			return true
		}
	}
	return false
}

// IsPending reports whether the agent is running, queued, or finished but not
// yet delivered.
func (s *Supervisor) IsPending(agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.jobs[agentID]
	return ok && !state.acked
}

func (s *Supervisor) pendingStates() []*jobState {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []*jobState
	for _, state := range s.jobs {
		if !state.acked {
			pending = append(pending, state)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].order < pending[j].order })
	return pending
}

// stateOf must be called with Supervisor.mu held.
func stateOf(state *jobState) agent.SubAgentState {
	switch {
	case state.completion != nil || state.phase == phaseDone:
		return agent.SubAgentFinished
	case state.phase == phaseQueued:
		return agent.SubAgentQueued
	default:
		return agent.SubAgentRunning
	}
}

// completeLocked records the final completion for a job.
func (s *Supervisor) completeLocked(state *jobState, result tool.ExecutionResult, err error) {
	if state.completion != nil {
		return
	}
	completion := s.newCompletionLocked(state)
	switch {
	case err != nil:
		completion.Status = string(StatusFailed)
		if state.childCtx.Err() != nil && state.cause != CancelCauseNone {
			completion.Status = string(StatusCancelled)
		}
		body, ok := agent.ProjectedToolError(err)
		if !ok {
			body = agent.FailureBody(completion.Status, err.Error())
		}
		completion.Body = body
	default:
		completion.Status = string(StatusComplete)
		if dr, ok := result.Value.(Result); ok {
			completion.Status = string(dr.Status)
			completion.TurnCount = dr.TurnCount
			completion.TokenCount = dr.TokenCount
		}
		body, ok := agent.ProjectedToolResult(result.Value)
		if !ok {
			completion.Status = string(StatusFailed)
			body = agent.FailureBody(completion.Status, errNoProjection.Error())
		}
		completion.Body = body
	}
	completion.Quiet = state.cause == CancelCauseUser && completion.Status == string(StatusCancelled)
	state.completion = completion
}

var errNoProjection = errors.New("sub-agent produced no projectable result")

func (s *Supervisor) newCompletionLocked(state *jobState) *agent.SubAgentCompletion {
	s.seq++
	completion := &agent.SubAgentCompletion{
		Seq:              s.seq,
		ParentCallID:     state.job.ParentCallID,
		AgentID:          state.job.AgentID,
		AgentType:        string(state.job.AgentType),
		ObjectivePreview: state.job.ObjectivePreview,
	}
	if !state.startedAt.IsZero() {
		completion.Duration = time.Since(state.startedAt)
	}
	return completion
}

// postList is a set of completion batches to hand to the sink after the
// supervisor lock is released.
type postList struct {
	sink    agent.CompletionSink
	batches [][]agent.SubAgentCompletion
}

func (p postList) deliver() {
	for _, batch := range p.batches {
		p.sink.DeliverCompletions(batch)
	}
}

// routeLocked decides what to post for a job that just gained a completion:
// ungrouped completions post at once, grouped ones are held until the group
// releases. Blocking jobs, a nil sink and a closed supervisor post nothing.
func (s *Supervisor) routeLocked(state *jobState) postList {
	if s.sink == nil || state.blocking || s.closed || state.completion == nil {
		return postList{}
	}
	if state.group == nil {
		return postList{sink: s.sink, batches: [][]agent.SubAgentCompletion{{*state.completion}}}
	}
	state.held = true
	return s.releaseGroupLocked(state.group)
}
