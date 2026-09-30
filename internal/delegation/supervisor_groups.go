package delegation

import (
	"sort"

	"github.com/luispabon/steiner/internal/agent"
)

// groupKey identifies a turn-scoped group: one label within one tool batch.
type groupKey struct {
	batch string
	label string
}

// jobGroup holds the members of one group in enqueue order. Guarded by Supervisor.mu.
type jobGroup struct {
	key     groupKey
	order   uint64
	sealed  bool
	members []*jobState
}

// enrollLocked joins an accepted job to its group when grouping applies:
// a completion sink is set and both the label and the tool batch are known.
func (s *Supervisor) enrollLocked(state *jobState, batchID string) {
	if s.sink == nil || state.job.Group == "" || batchID == "" {
		return
	}
	key := groupKey{batch: batchID, label: state.job.Group}
	group, ok := s.groups[key]
	if !ok {
		s.groupSeq++
		group = &jobGroup{key: key, order: s.groupSeq}
		s.groups[key] = group
	}
	group.members = append(group.members, state)
	state.group = group
}

// SealBatch closes every group of the tool batch to new members and releases
// those whose members have all finished. Groups settle in creation order.
func (s *Supervisor) SealBatch(batchID string) {
	s.mu.Lock()
	var sealed []*jobGroup
	for key, group := range s.groups {
		if key.batch == batchID {
			sealed = append(sealed, group)
		}
	}
	sort.Slice(sealed, func(i, j int) bool { return sealed[i].order < sealed[j].order })
	var posts postList
	for _, group := range sealed {
		group.sealed = true
		released := s.releaseGroupLocked(group)
		if len(released.batches) > 0 {
			posts.sink = released.sink
			posts.batches = append(posts.batches, released.batches...)
		}
	}
	s.mu.Unlock()

	posts.deliver()
}

// releaseGroupLocked posts a group's completions together, ordered by seq, once
// it is sealed and every member has a final result.
func (s *Supervisor) releaseGroupLocked(group *jobGroup) postList {
	if !group.sealed || s.sink == nil || s.closed {
		return postList{}
	}
	batch := make([]agent.SubAgentCompletion, 0, len(group.members))
	for _, member := range group.members {
		if member.completion == nil {
			return postList{}
		}
		batch = append(batch, *member.completion)
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].Seq < batch[j].Seq })
	for _, member := range group.members {
		member.held = false
	}
	delete(s.groups, group.key)
	return postList{sink: s.sink, batches: [][]agent.SubAgentCompletion{batch}}
}
