package oauth

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/oauth2"
)

// redactRefreshError reports a failed token request without quoting or exposing
// what the endpoint returned. oauth2 formats the response body into its retrieve
// errors and into the parse errors it reports for a 2xx body, so every
// non-context failure is reduced to a fixed category plus the HTTP status when
// one is known. A cancelled or timed-out request keeps its cause for errors.Is
// only: oauth2 reports a body read aborted by the context without wrapping the
// cause, so the refresh context is consulted as well.
func redactRefreshError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return &refreshCancelledError{cause: ctxErr}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &refreshCancelledError{cause: err}
	}
	redacted := &tokenEndpointError{}
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) && retrieve.Response != nil {
		redacted.status = retrieve.Response.StatusCode
	}
	return redacted
}

// tokenEndpointError names a failed token request by category and, when known,
// the HTTP status, which the endpoint does not control. It keeps no cause, so
// no response body is reachable through errors.Is or errors.As either.
type tokenEndpointError struct {
	status int
}

func (e *tokenEndpointError) Error() string {
	if e.status == 0 {
		return "token endpoint request failed"
	}
	return fmt.Sprintf("token endpoint request failed: status %d", e.status)
}

// refreshCancelledError reports a cancelled or timed-out token request without
// formatting the cause. Unwrap keeps the cause for errors.Is only; it is a
// context or transport error, which carries no response body.
type refreshCancelledError struct {
	cause error
}

func (e *refreshCancelledError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return "token request cancelled"
	}
	return "token request timed out"
}

func (e *refreshCancelledError) Unwrap() error { return e.cause }
