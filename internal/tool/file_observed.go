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
