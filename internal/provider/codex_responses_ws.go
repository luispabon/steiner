package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type codexWSProvider struct {
	apiKey       string
	headers      map[string]string
	model        string
	providerType string
	wsURL        string

	// streamErrorLog receives one provider-stream diagnostics record per
	// model call, regardless of outcome (issue #707). Nil is a valid no-op
	// logger, same convention as the HTTP Client.
	streamErrorLog *StreamErrorLogger

	// Liveness timings, defaulted from the wsPingInterval family so tests can
	// drive them in milliseconds.
	pingInterval      time.Duration
	interFrameTimeout time.Duration
	writeTimeout      time.Duration

	mu            sync.Mutex
	conn          *websocket.Conn
	connCancel    context.CancelFunc
	keepaliveDone chan struct{}
	dialCacheKey  string
}

// NewCodexResponsesWS constructs a Codex WebSocket provider. WebSocket failures
// return an error rather than degrading to HTTP: the transport is opt-in via
// codex.transport, so a caller who asked for it is told when it does not work
// instead of silently getting something else.
func NewCodexResponsesWS(cfg ClientConfig) (Provider, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	provider := &codexWSProvider{
		apiKey:            cfg.APIKey,
		headers:           copyHeaders(cfg.Headers),
		model:             cfg.Model,
		providerType:      cfg.ProviderType,
		streamErrorLog:    cfg.StreamErrorLog,
		wsURL:             WSEndpointURL,
		pingInterval:      wsPingInterval,
		interFrameTimeout: wsInterFrameTimeout,
		writeTimeout:      wsWriteTimeout,
	}

	return provider, nil
}

func (p *codexWSProvider) SupportsUsageStats() bool {
	return true
}

func (p *codexWSProvider) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	start := time.Now()
	// No sink: the unary path buffers the whole response, so a mid-stream
	// reconnect cannot duplicate anything the caller has seen.
	emitter := &wsEmitter{}
	p.mu.Lock()
	result, attempts, err := p.executeRequest(ctx, request, emitter)
	p.mu.Unlock()

	p.emitProviderCall(ctx, start, emitter, attempts, err)

	if err != nil {
		return ChatResponse{}, err
	}
	return result, nil
}

func (p *codexWSProvider) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	out := make(chan ChatChunk, 1)
	go func() {
		defer close(out)
		p.streamOnce(ctx, request, out)
	}()

	return out, nil
}

func (p *codexWSProvider) streamOnce(ctx context.Context, request ChatRequest, out chan<- ChatChunk) {
	start := time.Now()
	emitter := &wsEmitter{emit: func(chunk ChatChunk) error {
		select {
		case out <- chunk:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}

	p.mu.Lock()
	result, attempts, err := p.executeRequest(ctx, request, emitter)
	p.mu.Unlock()

	p.emitProviderCall(ctx, start, emitter, attempts, err)

	if err == nil {
		sendChunk(ctx, out, ChatChunk{
			Delta:        result.Message,
			Usage:        result.Usage,
			Done:         true,
			FinishReason: result.FinishReason,
		})
		return
	}

	sendChunk(ctx, out, ChatChunk{Done: true, Error: err.Error(), OriginalError: err})
}

// emitProviderCall writes one provider-stream diagnostics record for this
// call, whatever the outcome, mirroring Client.emitProviderCall for the
// WebSocket transport (issue #707).
func (p *codexWSProvider) emitProviderCall(ctx context.Context, start time.Time, emitter *wsEmitter, attempts int, err error) {
	var ttft time.Duration
	if !emitter.firstEmitAt.IsZero() {
		ttft = emitter.firstEmitAt.Sub(start)
	}
	emitProviderCall(providerCallInput{
		log:       p.streamErrorLog,
		provider:  p.providerType,
		model:     p.model,
		transport: "ws",
		start:     start,
		ttft:      ttft,
		attempts:  attempts,
		chunks:    emitter.count,
		// partial_stream means visible content arrived but the call did not
		// complete: true on every successful stream would carry no information.
		partialStream:  emitter.emitted && err != nil,
		err:            err,
		ctx:            ctx,
		requestURL:     p.wsURL,
		requestHeaders: sanitizeHeaderMap(p.headers),
	})
}

// sendChunk delivers a terminal chunk, giving up if the consumer has gone away
// so an abandoned stream cannot wedge the provider.
func sendChunk(ctx context.Context, out chan<- ChatChunk, chunk ChatChunk) {
	select {
	case out <- chunk:
	case <-ctx.Done():
	}
}

// executeRequest sends one request, reconnecting once if the connection turns
// out to be dead. Callers must hold p.mu. The returned int is the number of
// times sendRequest was actually invoked, for the provider diagnostics
// record's attempts field.
func (p *codexWSProvider) executeRequest(ctx context.Context, request ChatRequest, emitter *wsEmitter) (ChatResponse, int, error) {
	var reconnectAttempt bool
	sendAttempts := 0

	for i := 0; i < 2; i++ {
		if err := p.ensureConnection(ctx, request); err != nil {
			if reconnectAttempt {
				return ChatResponse{}, sendAttempts, fmt.Errorf("reconnect failed: %w", err)
			}
			if ctx.Err() != nil {
				return ChatResponse{}, sendAttempts, err
			}
			recordWSTelemetry(wsTelemetryEventReconnect, "dial: "+err.Error(), p.dialCacheKey)
			reconnectAttempt = true
			p.closeConn()
			continue
		}

		sendAttempts++
		result, err := p.sendRequest(ctx, request, emitter)
		if err == nil {
			return result, sendAttempts, nil
		}

		// Retrying resends the whole request on a fresh connection, so the
		// response starts over. That is only safe while nothing has been
		// emitted; after the first delta a retry would repeat text the consumer
		// already has, so the turn fails instead. A cancelled caller is not a
		// dead connection either: redialling on a dead context would only log a
		// reconnect that never happened.
		if reconnectAttempt || emitter.emitted || ctx.Err() != nil {
			p.closeConn()
			return ChatResponse{}, sendAttempts, err
		}

		recordWSTelemetry(wsTelemetryEventReconnect, "request: "+err.Error(), p.dialCacheKey)
		reconnectAttempt = true
		p.closeConn()
	}

	return ChatResponse{}, sendAttempts, fmt.Errorf("failed after reconnect attempt")
}
