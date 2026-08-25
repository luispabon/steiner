package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

func (p *codexWSProvider) ensureConnection(ctx context.Context, request ChatRequest) error {
	if p.conn != nil {
		return nil
	}

	if p.dialCacheKey == "" {
		p.dialCacheKey = request.PromptCacheKey
	}

	headers := buildWSHeaders(p.apiKey, p.headers, p.dialCacheKey)
	conn, resp, err := websocket.Dial(ctx, p.wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("dial WebSocket: %w", err)
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
