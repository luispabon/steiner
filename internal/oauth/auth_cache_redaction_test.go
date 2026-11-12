package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// tokenEndpointFailureServer answers every token request with body under
// status, so a refresh has to report the endpoint's answer.
func tokenEndpointFailureServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestAuthResolverRefreshErrorRedactsTokenEndpointBody proves a rejected
// refresh reports the endpoint status without quoting its response, while the
// endpoint error stays reachable through errors.As.
func TestAuthResolverRefreshErrorRedactsTokenEndpointBody(t *testing.T) {
	const secret = "secret-refresh-credential"

	cases := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "unparsable body",
			contentType: "application/json",
			body:        `{"credential":` + secret,
		},
		{
			name:        "error description",
			contentType: "application/json",
			body: fmt.Sprintf(
				`{"error":"invalid_grant","error_description":"%s rejected","access_token":"%s"}`,
				secret, secret),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tokenEndpointFailureServer(t, http.StatusBadRequest, tc.contentType, tc.body)
			store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
			saveRefreshDueTestToken(t, store)

			_, err := NewAuthResolver().Resolve(context.Background(), store, testTokenConf(server.URL), nil)
			if err == nil {
				t.Fatal("Resolve() error = nil, want the rejected refresh")
			}
			if strings.Contains(err.Error(), secret) {
				// The message itself would leak the secret, so it is not quoted.
				t.Error("Resolve() error quotes the token endpoint response")
				return
			}
			if !strings.Contains(err.Error(), "status 400") {
				t.Errorf("Resolve() error = %q, want the endpoint status in the message", err)
			}
			var retrieve *oauth2.RetrieveError
			if !errors.As(err, &retrieve) {
				t.Errorf("Resolve() error = %v, want errors.As to reach the endpoint failure", err)
			}
		})
	}
}

// cancelTransport fails every request once its context ends, which is how a
// refresh sees its own deadline.
type cancelTransport struct{}

func (cancelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestAuthResolverRefreshCancellationStaysClassifiable proves the redacted
// refresh path still reports its timeout as a cancellation.
func TestAuthResolverRefreshCancellationStaysClassifiable(t *testing.T) {
	store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
	saveRefreshDueTestToken(t, store)

	resolver := NewAuthResolver()
	resolver.refreshTimeout = 20 * time.Millisecond
	client := &http.Client{Transport: cancelTransport{}}

	_, err := resolver.Resolve(context.Background(), store, testTokenConf("http://token.invalid/oauth/token"), client)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resolve() error = %v, want context.DeadlineExceeded", err)
	}
}
