package advisor

import (
	"github.com/luispabon/steiner/internal/provider"
)

// maxReductionScopes bounds the number of conversation ledgers kept in one
// SharedState. A process can run the main advisor and several child advisors,
// and interactive sessions can switch conversations; eight scopes keeps the
// common cases resident without unbounded growth.
const maxReductionScopes = 8

// reductionStage identifies how a frozen tool result was reduced.
// reductionStageNone means the message is unchanged.
type reductionStage int

const (
	reductionStageNone reductionStage = iota
	reductionStageReadDedupe
	reductionStageClip
	reductionStageStub
)

func (s reductionStage) String() string {
	switch s {
	case reductionStageReadDedupe:
		return "read_dedupe"
	case reductionStageClip:
		return "clip"
	case reductionStageStub:
		return "stub"
	default:
		return "none"
	}
}

// reductionLedgerEntry freezes one transcript source message. SourceHash
// identifies the original message so appended conversations replay the exact
// replacement instead of reclipping. Replacement is set only when the message
// was changed.
type reductionLedgerEntry struct {
	sourceHash  string
	replacement string
	stage       reductionStage
}

func (e reductionLedgerEntry) replaced() bool {
	return e.stage != reductionStageNone
}

// reductionLedgerState is an immutable copy of one conversation's frozen
// replacements plus its exhaustion bookkeeping.
type reductionLedgerState struct {
	entries         []reductionLedgerEntry
	epoch           uint64
	exhausted       bool
	exhaustedTokens int
}

func (s reductionLedgerState) clone() reductionLedgerState {
	out := s
	if len(s.entries) > 0 {
		out.entries = append([]reductionLedgerEntry(nil), s.entries...)
	}
	return out
}

// reductionLedger returns a copy of the ledger for scope, creating an empty
// entry and refreshing its LRU position. The caller never holds the lock while
// hashing, estimating, or planning.
func (s *SharedState) reductionLedger(scope string) reductionLedgerState {
	s.reductionMu.Lock()
	defer s.reductionMu.Unlock()
	s.touchReductionScopeLocked(scope)
	if ledger, ok := s.reductions[scope]; ok {
		return ledger.clone()
	}
	return reductionLedgerState{}
}

// storeReductionLedger replaces the ledger for scope when expectedEpoch still
// matches, returning false when another caller committed first. It is the
// compare-and-swap commit point; no plan work happens under the lock.
func (s *SharedState) storeReductionLedger(scope string, expectedEpoch uint64, next reductionLedgerState) bool {
	s.reductionMu.Lock()
	defer s.reductionMu.Unlock()
	if s.reductions == nil {
		s.reductions = make(map[string]*reductionLedgerState)
	}
	current := s.reductions[scope]
	if current == nil {
		if expectedEpoch != 0 {
			return false
		}
		current = &reductionLedgerState{}
	}
	if current.epoch != expectedEpoch {
		return false
	}
	stored := next.clone()
	stored.epoch = current.epoch + 1
	s.reductions[scope] = &stored
	s.touchReductionScopeLocked(scope)
	return true
}

func (s *SharedState) touchReductionScopeLocked(scope string) {
	for i, existing := range s.reductionOrder {
		if existing == scope {
			s.reductionOrder = append(s.reductionOrder[:i], s.reductionOrder[i+1:]...)
			break
		}
	}
	s.reductionOrder = append(s.reductionOrder, scope)
	for len(s.reductionOrder) > maxReductionScopes {
		oldest := s.reductionOrder[0]
		s.reductionOrder = s.reductionOrder[1:]
		if oldest != scope {
			delete(s.reductions, oldest)
		}
	}
}

// reductionReplay is the result of applying a ledger to the current transcript.
type reductionReplay struct {
	transcript []provider.Message
	stages     []reductionStage
	// hashes are the source hashes of every current transcript message.
	hashes []string
	// valid is the number of ledger entries that still match the transcript.
	valid int
	// changed reports whether at least one frozen replacement was applied.
	changed bool
}

// replayReductionLedger matches entries against transcript by source hash and
// applies frozen replacements up to the first mismatch. It never mutates the
// input messages.
func replayReductionLedger(transcript []provider.Message, entries []reductionLedgerEntry) reductionReplay {
	replay := reductionReplay{
		transcript: append([]provider.Message(nil), transcript...),
		stages:     make([]reductionStage, len(transcript)),
		hashes:     make([]string, len(transcript)),
	}
	for i, message := range transcript {
		replay.hashes[i] = messageHash(canonicalMessage(message))
	}
	limit := min(len(entries), len(transcript))
	replay.valid = limit
	for i := 0; i < limit; i++ {
		if entries[i].sourceHash != replay.hashes[i] {
			replay.valid = i
			break
		}
	}
	for i := 0; i < replay.valid; i++ {
		if !entries[i].replaced() || transcript[i].Role != provider.MessageRoleTool {
			continue
		}
		replay.transcript[i].Content = entries[i].replacement
		replay.stages[i] = entries[i].stage
		replay.changed = true
	}
	return replay
}
