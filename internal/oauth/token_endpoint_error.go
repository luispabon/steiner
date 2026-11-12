package oauth

import (
	"errors"
	"fmt"

	"golang.org/x/oauth2"
)

// redactTokenEndpointError reports a failed token endpoint request without
// quoting the endpoint's response. oauth2.RetrieveError formats the response
// body, and an error response to a token request can carry credential
// material, so the cause is kept for errors.Is and errors.As only and never
// formatted into the message. Other failures are returned unchanged, which
// keeps a cancelled or timed-out request recognizable.
func redactTokenEndpointError(err error) error {
	var retrieve *oauth2.RetrieveError
	if !errors.As(err, &retrieve) {
		return err
	}
	redacted := &tokenEndpointError{err: err}
	if retrieve.Response != nil {
		redacted.status = retrieve.Response.StatusCode
	}
	return redacted
}

// tokenEndpointError names a rejected token endpoint request by HTTP status,
// which the endpoint does not control. Its message never quotes the response,
// while Unwrap keeps the endpoint error reachable through errors.Is and
// errors.As.
type tokenEndpointError struct {
	err    error
	status int
}

func (e *tokenEndpointError) Error() string {
	if e.status == 0 {
		return "token endpoint request failed"
	}
	return fmt.Sprintf("token endpoint request failed: status %d", e.status)
}

func (e *tokenEndpointError) Unwrap() error { return e.err }
