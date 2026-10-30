package provider

import "errors"

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
