package advisor

import (
	"strings"

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
// was changed. ReferenceIndex points at the earlier read a duplicate note
// refers to, or -1 when the replacement carries no reference.
type reductionLedgerEntry struct {
	sourceHash     string
	replacement    string
	stage          reductionStage
	referenceIndex int
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
	// exhaustedLen is the transcript length the exhaustion was recorded
	// against and exhaustedDigest identifies that source prefix. Exhaustion
	// suppression only holds while the current transcript still extends the
	// exact same prefix, so a shrunk or edited conversation is replanned
	// instead of staying suppressed.
	exhaustedLen    int
	exhaustedDigest string
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
	if ledger, ok := s.reductions[scope]; ok {
		s.touchReductionScopeLocked(scope)
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
	// source is the caller's transcript. It is never mutated.
	source []provider.Message
	// transcript applies frozen replacements. It aliases source until the
	// first replacement is applied, then becomes a copy.
	transcript []provider.Message
	stages     []reductionStage
	// references mirrors stages: the earlier read index a dedupe note points
	// to, or -1. It is allocated only when a replayed dedupe note needs it.
	references []int
	// hashes holds the source hashes computed so far, in transcript order.
	// Hashing is lazy: only the prefix validated against the ledger is hashed.
	hashes []string
	// valid is the number of ledger entries that still match the transcript.
	valid int
}

// ensureHashes computes source hashes up to n transcript messages.
func (r *reductionReplay) ensureHashes(n int) {
	n = min(n, len(r.source))
	for len(r.hashes) < n {
		r.hashes = append(r.hashes, messageHash(canonicalMessage(r.source[len(r.hashes)])))
	}
}

// sourceHashes returns a hash for every source message, filling any that the
// lazy replay left unhashed.
func (r *reductionReplay) sourceHashes() []string {
	r.ensureHashes(len(r.source))
	return r.hashes
}

// replayReductionLedger matches entries against transcript by source hash and
// applies frozen replacements up to the first mismatch. It never mutates the
// input messages.
func replayReductionLedger(transcript []provider.Message, entries []reductionLedgerEntry) reductionReplay {
	replay := reductionReplay{
		source:     transcript,
		transcript: transcript,
		stages:     make([]reductionStage, len(transcript)),
	}
	limit := min(len(entries), len(transcript))
	replay.valid = limit
	for i := 0; i < limit; i++ {
		replay.ensureHashes(i + 1)
		if entries[i].sourceHash != replay.hashes[i] {
			replay.valid = i
			break
		}
	}
	cloned := false
	for i := 0; i < replay.valid; i++ {
		if !entries[i].replaced() || transcript[i].Role != provider.MessageRoleTool {
			continue
		}
		if !cloned {
			replay.transcript = append([]provider.Message(nil), transcript...)
			cloned = true
		}
		replay.transcript[i].Content = entries[i].replacement
		replay.stages[i] = entries[i].stage
		if replay.references == nil {
			replay.references = make([]int, len(transcript))
			for j := range replay.references {
				replay.references[j] = -1
			}
		}
		replay.references[i] = entries[i].referenceIndex
	}
	return replay
}

// exhaustionDigest identifies the source-hash prefix of replay that an
// exhaustion was recorded against. Hashes are fixed width, so joining them
// keeps distinct prefixes from colliding.
func exhaustionDigest(replay *reductionReplay, n int) string {
	replay.ensureHashes(n)
	return messageHash(strings.Join(replay.hashes[:min(n, len(replay.hashes))], ""))
}

// coversTranscript reports whether the current transcript still extends the
// exact source prefix that was exhausted. A shrinking conversation or an
// edited interior message no longer covers it, so it is replanned.
func (s reductionLedgerState) coversTranscript(replay *reductionReplay) bool {
	if len(replay.source) < s.exhaustedLen {
		return false
	}
	if s.exhaustedLen == 0 {
		return len(replay.source) == 0
	}
	return exhaustionDigest(replay, s.exhaustedLen) == s.exhaustedDigest
}
