package tool

import "context"

// FileObservedChecker reports whether path was observed this session (e.g.
// via a read tool result). It backs the mutate replace-operation guard,
// which requires either an observed read or an explicit file_hash before
// accepting a blind edit against assumed file contents.
type FileObservedChecker func(path string) bool

// fileObservedCheckerKey is the context key carrying the checker into tool
// handlers, mirroring approvalAgentScopeKey/EffectivePolicyKey.
type fileObservedCheckerKey struct{}

// WithFileObservedChecker returns a context carrying checker so tool
// handlers can look up whether a given path was observed this session. A nil
// checker returns ctx unchanged.
func WithFileObservedChecker(ctx context.Context, checker FileObservedChecker) context.Context {
	if checker == nil {
		return ctx
	}
	return context.WithValue(ctx, fileObservedCheckerKey{}, checker)
}

// FileObservedCheckerFromContext returns the checker attached by
// WithFileObservedChecker, or nil when absent.
func FileObservedCheckerFromContext(ctx context.Context) FileObservedChecker {
	checker, _ := ctx.Value(fileObservedCheckerKey{}).(FileObservedChecker)
	return checker
}

// FileReadState is the agent's record of its last read of a path, as seen at
// lookup time. It backs mutate's failure diagnostics only; it never changes
// mutate behaviour.
type FileReadState struct {
	Known            bool // a lookup was available; false means "unknown"
	Observed         bool // a read is on record
	Pruned           bool // a read existed but compaction pruned it
	StartLine        int
	EndLine          int
	TotalLines       int
	TurnsSinceRead   int  // current turn - read turn; -1 when not observed
	MutatedSinceRead bool // the agent's own mutation bumped the generation after the read
	ChangedSinceRead bool // on-disk content hash now differs from the hash at read
}

// FileReadLookup returns the agent's read state for path at lookup time.
type FileReadLookup func(path string) FileReadState

// fileReadLookupKey is the context key carrying the lookup into tool handlers,
// mirroring fileObservedCheckerKey.
type fileReadLookupKey struct{}

// WithFileReadLookup returns a context carrying lookup so tool handlers can
// inspect the agent's last read of a path.
func WithFileReadLookup(ctx context.Context, lookup FileReadLookup) context.Context {
	return context.WithValue(ctx, fileReadLookupKey{}, lookup)
}

// FileReadLookupFromContext returns the lookup attached by WithFileReadLookup,
// or nil when absent.
func FileReadLookupFromContext(ctx context.Context) FileReadLookup {
	lookup, _ := ctx.Value(fileReadLookupKey{}).(FileReadLookup)
	return lookup
}
