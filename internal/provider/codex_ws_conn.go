package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Liveness timings for the Codex WebSocket transport. A Codex connection is
// closed by the backend after roughly 5 minutes of idleness (measured), and
// nothing in the WebSocket API reports that: a non-nil conn looks live until
// the next write or read fails. Sub-agent calls leave the parent's socket idle
// for their whole duration, which is how the idle close turned into a
// multi-minute user-visible stall.
const (
	// wsPingInterval keeps the socket warm well inside the measured ~5 minute
	// idle window: four pings fit in one window, so several can be lost before
	// the backend would consider the connection idle.
	wsPingInterval = 60 * time.Second

	// wsPingTimeout bounds one keepalive ping. Ping also waits for the pong,
	// which coder/websocket only delivers while a Reader is active, so an idle
	// keepalive always times out on that wait. That is expected and harmless:
	// the outbound ping frame is what resets the backend's idle timer. Because
	// the wait blocks the loop, the effective idle ping period is the interval
	// plus this bound — 70s against the ~5 minute threshold.
	wsPingTimeout = 10 * time.Second

	// wsInterFrameTimeout bounds the gap between two frames of one response
	// (and, on the first read, the gap from the request write to the first
	// frame), not the response as a whole. A total-response deadline would
	// truncate long reasoning; this one cannot, because deltas arrive
	// continuously, so a gap this large means the stream is stalled rather than
	// slow. It also sits below the idle-close threshold, so a socket that died
	// mid-response surfaces as an error instead of a hang.
	wsInterFrameTimeout = 120 * time.Second

	// wsWriteTimeout caps a write on a half-open socket, where the peer is gone
	// but the kernel keeps retransmitting with no deadline of its own. It is a
	// bound on that hang, not a latency budget.
	wsWriteTimeout = 30 * time.Second
)

var (
	// errCodexSignInChanged reports a stored sign-in whose auth mode or account
	// differs from the one the provider was built with, so the provider's base
	// URL, account header and quota key no longer describe it.
	errCodexSignInChanged = errors.New("stored Codex sign-in changed account or auth mode")
	// errCodexSignInNoToken reports a stored sign-in that yields no bearer
	// credential at all.
	errCodexSignInNoToken = errors.New("stored Codex sign-in has no access token")
)

// wsAuthError marks a dial that failed while resolving credentials. The request
// path reports it without retrying, because a second dial would resolve the
// same credentials again, and it keeps the cause reachable through errors.Is so
// a cancellation stays recognizable.
type wsAuthError struct{ err error }

func (e *wsAuthError) Error() string { return "codex websocket credentials: " + e.err.Error() }

func (e *wsAuthError) Unwrap() error { return e.err }

// wsDialAuth resolves the bearer credential for each Codex WebSocket dial. It
// captures the sign-in identity the provider was built from, because the base
// URL, ChatGPT-Account-ID header and quota key all belong to that identity: a
// resolver that reports another account or auth mode fails the dial instead of
// pairing the new credentials with stale identity headers.
type wsDialAuth struct {
	resolver   func(context.Context) (CodexAuth, error)
	apiMode    bool
	accountKey string
}

// newWSDialAuth captures the identity of the sign-in cfg was built from. The
// CLI sets a ChatGPT-Account-ID header only for a ChatGPT sign-in, so its
// absence marks API-key mode.
func newWSDialAuth(cfg ClientConfig) *wsDialAuth {
	accountKey := codexQuotaAccountKeyFromHeaders(cfg.Headers)
	return &wsDialAuth{
		resolver:   cfg.DialBearerResolver,
		apiMode:    accountKey == "",
		accountKey: accountKey,
	}
}

// bearer resolves the credential for one dial and returns the bearer to send.
// The error never contains credential material.
func (a *wsDialAuth) bearer(ctx context.Context) (string, error) {
	auth, err := a.resolver(ctx)
	if err != nil {
		return "", &wsAuthError{err: err}
	}
	apiMode := auth.APIKey != ""
	if apiMode != a.apiMode {
		return "", &wsAuthError{err: errCodexSignInChanged}
	}
	// An API-key request sends no account header, so only a ChatGPT sign-in has
	// an account identity to keep.
	if apiMode {
		return auth.APIKey, nil
	}
	if wsAccountKey(auth.AccountID) != a.accountKey {
		return "", &wsAuthError{err: errCodexSignInChanged}
	}
	if auth.AccessToken == "" {
		return "", &wsAuthError{err: errCodexSignInNoToken}
	}
	return auth.AccessToken, nil
}

// wsAccountKey returns the quota account key of a ChatGPT account ID, in the
// form codexQuotaAccountKeyFromHeaders derives from headers.
func wsAccountKey(accountID string) string {
	if accountID == "" {
		return ""
	}
	return CodexQuotaAccountKey(accountID)
}

func (p *codexWSProvider) ensureConnection(ctx context.Context, request ChatRequest) error {
	if p.conn != nil {
		return nil
	}

	if p.dialCacheKey == "" {
		p.dialCacheKey = request.PromptCacheKey
	}

	apiKey := p.apiKey
	if p.dialAuth != nil {
		resolved, err := p.dialAuth.bearer(ctx)
		if err != nil {
			return err
		}
		apiKey = resolved
	}

	headers := buildWSHeaders(apiKey, p.headers, p.dialCacheKey)
	conn, resp, err := websocket.Dial(ctx, p.wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("dial WebSocket: %w", err)
	}
	// The handshake headers carry the rate limits once per connection.
	if p.quotaAccountKey != "" {
		if snap, ok := parseCodexQuotaHeaders(resp.Header, p.quotaAccountKey); ok {
			p.codexQuotaQueue(snap)
		}
	}

	conn.SetReadLimit(WSReadLimitBytes)
	recordWSTelemetry(wsTelemetryEventDial, "", p.dialCacheKey)

	// The keepalive outlives the dialling request, so it runs on a context
	// detached from it, and it is handed the connection by value: it never
	// reads p.conn, so it cannot race the request path.
	keepaliveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go p.runKeepalive(keepaliveCtx, conn, done)

	p.conn = conn
	p.connCancel = cancel
	p.keepaliveDone = done
	return nil
}

// enterRequest records that a request is about to use the connection. It must
// run before the request takes p.mu, and leaveRequest after it drops p.mu, on
// the goroutine that performs the socket I/O, so a concurrent release can tell
// an idle connection from one in use.
func (p *codexWSProvider) enterRequest() {
	p.gateMu.Lock()
	p.inFlight++
	p.gateMu.Unlock()
}

// leaveRequest ends the request enterRequest began. The last request out closes
// a connection whose release arrived while it ran. The gate stays held across
// the close, so a request starting at that moment cannot dial into a connection
// the close is about to drop. closeConn joins the keepalive goroutine, which
// takes neither p.mu nor the gate.
func (p *codexWSProvider) leaveRequest() {
	p.gateMu.Lock()
	defer p.gateMu.Unlock()

	p.inFlight--
	if p.inFlight > 0 || !p.releasePending {
		return
	}
	p.releasePending = false
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeConn()
}

// ReleaseConnection closes the connection and stops its keepalive, leaving the
// provider usable: the next request dials again with the same affinity headers.
// A request in flight is not interrupted; the connection closes when that
// request ends, so a release returns promptly either way. Part of
// provider.ConnectionReleaser; the cached wrapper deliberately omits it so the
// cache keeps ownership of cached connections.
func (p *codexWSProvider) ReleaseConnection() {
	p.gateMu.Lock()
	defer p.gateMu.Unlock()

	if p.inFlight > 0 {
		p.releasePending = true
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeConn()
}

// closeConn drops the current connection and its keepalive. Every path that
// discards the connection goes through here, so the goroutine can never outlive
// the socket it pings. Callers must hold p.mu.
func (p *codexWSProvider) closeConn() {
	if p.connCancel != nil {
		p.connCancel()
		<-p.keepaliveDone
		p.connCancel = nil
		p.keepaliveDone = nil
	}
	if p.conn != nil {
		_ = p.conn.CloseNow()
		p.conn = nil
	}
}

// runKeepalive pings conn until its context is cancelled by closeConn. It does
// not own the connection, so a failed ping does not tear it down: the next
// bounded write or read on the request path reports the death, and a pong wait
// that expires on an idle socket is not a failure at all.
func (p *codexWSProvider) runKeepalive(ctx context.Context, conn *websocket.Conn, done chan struct{}) {
	defer close(done)

	ticker := time.NewTicker(p.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		pingCtx, cancel := context.WithTimeout(ctx, min(wsPingTimeout, p.pingInterval))
		err := conn.Ping(pingCtx)
		cancel()
		if err != nil {
			slog.Debug("codex websocket keepalive ping", "error", err)
		}
	}
}

func buildWSHeaders(apiKey string, headers map[string]string, cacheKey string) http.Header {
	result := http.Header{
		"OpenAI-Beta":           {WSBetaHeaderValue},
		WSHeaderInstallationID:  {"default"},
		WSHeaderClientRequestID: {generateClientRequestID()},
	}

	if strings.TrimSpace(apiKey) != "" {
		result.Set("Authorization", "Bearer "+apiKey)
	}

	if cacheKey != "" {
		result.Set("session-id", cacheKey)
		result.Set("thread-id", cacheKey)
		result.Set("originator", "codex_cli_rs")
	}

	for key, value := range headers {
		result.Set(key, value)
	}

	return result
}

func generateClientRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "cli-error"
	}
	return "cli-" + hex.EncodeToString(b)
}
