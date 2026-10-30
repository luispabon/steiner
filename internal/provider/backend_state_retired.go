package provider

import (
	"context"
	"errors"
	"sync/atomic"
)

// backendStateRetiredError marks an error whose request retired the backend
// state it was issued against. It keeps the original error text and chain, so
// errors.Is and errors.As still match the underlying failure.
type backendStateRetiredError struct{ err error }

func (e *backendStateRetiredError) Error() string { return e.err.Error() }

func (e *backendStateRetiredError) Unwrap() error { return e.err }

// BackendStateRetired reports that the request retired its backend state.
func (e *backendStateRetiredError) BackendStateRetired() bool { return true }

// BackendStateRetired reports whether err comes from a request that retired the
// backend state stored for its generation. The caller must drop that state
// instead of reusing it, even though the error text is the original failure.
func BackendStateRetired(err error) bool {
	var marked interface{ BackendStateRetired() bool }
	return errors.As(err, &marked) && marked.BackendStateRetired()
}

// markBackendStateRetired wraps err as a retired backend state error. It
// returns nil unchanged and leaves err's text and chain intact.
func markBackendStateRetired(err error) error {
	if err == nil {
		return nil
	}
	return &backendStateRetiredError{err: err}
}

// backendStateRetirementKey keys the request-scoped retirement signal in a
// context.
type backendStateRetirementKey struct{}

// backendStateRetirement is the request-scoped flag a turn sets once it has
// retired the backend state issued for the request. The flag is atomic because
// the turn sets it while the caller may already be reading it.
type backendStateRetirement struct{ retired atomic.Bool }

// WithBackendStateRetirement returns a copy of ctx carrying a request-scoped
// retirement signal. The provider marks it when the request retires the backend
// state it was issued against, so the caller learns that even when the terminal
// chunk is lost. Read it with BackendStateRetirementSignaled.
func WithBackendStateRetirement(ctx context.Context) context.Context {
	return context.WithValue(ctx, backendStateRetirementKey{}, &backendStateRetirement{})
}

// HasBackendStateRetirement reports whether ctx carries a request-scoped
// retirement signal, whether or not it has been signalled yet. A caller that
// sees a processing error can use it to decide whether waiting for the request
// to finish can reveal a retirement the error itself did not report.
func HasBackendStateRetirement(ctx context.Context) bool {
	return backendStateRetirementFrom(ctx) != nil
}

// BackendStateRetirementSignaled reports whether the request run under ctx
// retired its backend state. It is false for a context that carries no signal.
func BackendStateRetirementSignaled(ctx context.Context) bool {
	r := backendStateRetirementFrom(ctx)
	return r != nil && r.retired.Load()
}

// backendStateRetirementFrom returns the retirement signal carried by ctx, or
// nil when ctx carries none.
func backendStateRetirementFrom(ctx context.Context) *backendStateRetirement {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(backendStateRetirementKey{}).(*backendStateRetirement)
	return r
}

// signalBackendStateRetirement marks the retirement signal carried by ctx. A
// context without a signal is left alone.
func signalBackendStateRetirement(ctx context.Context) {
	if r := backendStateRetirementFrom(ctx); r != nil {
		r.retired.Store(true)
	}
}
