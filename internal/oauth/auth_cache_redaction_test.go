package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// tokenEndpointBodyServer answers every token request with body under status,
// so a refresh has to report the endpoint's answer.
func tokenEndpointBodyServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestAuthResolverRefreshErrorRedactsTokenEndpointBody proves a failed refresh
// reports a fixed category, plus the status of a rejected request, without
// quoting or otherwise exposing what the token endpoint returned.
func TestAuthResolverRefreshErrorRedactsTokenEndpointBody(t *testing.T) {
	const secret = "secret-refresh-credential"
	const fixed = "refresh token: token endpoint request failed"

	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
		want        string
	}{
		{
			name:        "unparsable error body",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"credential":` + secret,
			want:        fixed + ": status 400",
		},
		{
			name:        "error description",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"error":"invalid_grant","error_description":"` + secret + ` rejected","access_token":"` + secret + `"}`,
			want:        fixed + ": status 400",
		},
		{
			name:        "malformed json success",
			status:      http.StatusOK,
			contentType: "application/json",
			body:        `{"access_token":"` + secret,
			want:        fixed,
		},
		{
			name:        "malformed form success",
			status:      http.StatusOK,
			contentType: "application/x-www-form-urlencoded",
			body:        "access_token=" + secret + "%zz",
			want:        fixed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tokenEndpointBodyServer(t, tc.status, tc.contentType, tc.body)
			store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
			saveRefreshDueTestToken(t, store)

			_, err := NewAuthResolver().Resolve(context.Background(), store, testTokenConf(server.URL), nil)
			if err == nil {
				t.Fatal("Resolve() error = nil, want the failed refresh")
			}
			if strings.Contains(err.Error(), secret) {
				// The message itself would leak the secret, so it is not quoted.
				t.Error("Resolve() error quotes the token endpoint response")
				return
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("Resolve() error = %q, want %q", got, tc.want)
			}
			var retrieve *oauth2.RetrieveError
			if errors.As(err, &retrieve) {
				t.Error("Resolve() error exposes the endpoint failure, whose body can hold credentials")
			}
		})
	}
}

// TestAuthResolverRefreshCancellationStaysClassifiable proves a cancelled or
// timed-out token request stays recognizable through errors.Is while its
// message stays a fixed category that does not format the cause.
func TestAuthResolverRefreshCancellationStaysClassifiable(t *testing.T) {
	cases := []struct {
		name    string
		trans   roundTripFunc
		wantIs  error
		wantMsg string
	}{
		{
			name: "request context deadline",
			trans: func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			},
			wantIs:  context.DeadlineExceeded,
			wantMsg: "refresh token: token request timed out",
		},
		{
			name: "transport cancellation",
			trans: func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			},
			wantIs:  context.Canceled,
			wantMsg: "refresh token: token request cancelled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
			saveRefreshDueTestToken(t, store)

			resolver := NewAuthResolver()
			resolver.refreshTimeout = 20 * time.Millisecond

			_, err := resolver.Resolve(context.Background(), store, testTokenConf("http://token.invalid/oauth/token"), &http.Client{Transport: tc.trans})
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("Resolve() error = %v, want %v", err, tc.wantIs)
			}
			if got := err.Error(); got != tc.wantMsg {
				t.Errorf("Resolve() error = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

// TestAuthResolverRefreshBodyReadDeadlineStaysClassifiable proves a deadline
// that aborts the response body read, which oauth2 reports without wrapping,
// is still classified as a cancellation.
func TestAuthResolverRefreshBodyReadDeadlineStaysClassifiable(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"partial`)
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	store := NewTokenStore(filepath.Join(t.TempDir(), "token.json"))
	saveRefreshDueTestToken(t, store)

	resolver := NewAuthResolver()
	resolver.refreshTimeout = 50 * time.Millisecond

	_, err := resolver.Resolve(context.Background(), store, testTokenConf(server.URL), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resolve() error = %v, want context.DeadlineExceeded", err)
	}
	if got := err.Error(); got != "refresh token: token request timed out" {
		t.Errorf("Resolve() error = %q, want the fixed category", got)
	}
}
