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
	apiKey  string
	headers map[string]string
	model   string
	wsURL   string

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
	p.mu.Lock()
	// No sink: the unary path buffers the whole response, so a mid-stream
	// reconnect cannot duplicate anything the caller has seen.
	result, err := p.executeRequest(ctx, request, &wsEmitter{})
	p.mu.Unlock()

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
	emitter := &wsEmitter{emit: func(chunk ChatChunk) error {
		select {
		case out <- chunk:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}

	p.mu.Lock()
	result, err := p.executeRequest(ctx, request, emitter)
	p.mu.Unlock()

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

// sendChunk delivers a terminal chunk, giving up if the consumer has gone away
// so an abandoned stream cannot wedge the provider.
func sendChunk(ctx context.Context, out chan<- ChatChunk, chunk ChatChunk) {
	select {
	case out <- chunk:
	case <-ctx.Done():
	}
}

// executeRequest sends one request, reconnecting once if the connection turns
// out to be dead. Callers must hold p.mu.
func (p *codexWSProvider) executeRequest(ctx context.Context, request ChatRequest, emitter *wsEmitter) (ChatResponse, error) {
	var reconnectAttempt bool

	for attempts := 0; attempts < 2; attempts++ {
		if err := p.ensureConnection(ctx, request); err != nil {
			if reconnectAttempt {
				return ChatResponse{}, fmt.Errorf("reconnect failed: %w", err)
			}
			if ctx.Err() != nil {
				return ChatResponse{}, err
			}
			recordWSTelemetry(wsTelemetryEventReconnect, "dial: "+err.Error(), p.dialCacheKey)
			reconnectAttempt = true
			p.closeConn()
			continue
		}

		result, err := p.sendRequest(ctx, request, emitter)
		if err == nil {
			return result, nil
		}

		// Retrying resends the whole request on a fresh connection, so the
		// response starts over. That is only safe while nothing has been
		// emitted; after the first delta a retry would repeat text the consumer
		// already has, so the turn fails instead. A cancelled caller is not a
		// dead connection either: redialling on a dead context would only log a
		// reconnect that never happened.
		if reconnectAttempt || emitter.emitted || ctx.Err() != nil {
			p.closeConn()
			return ChatResponse{}, err
		}

		recordWSTelemetry(wsTelemetryEventReconnect, "request: "+err.Error(), p.dialCacheKey)
		reconnectAttempt = true
		p.closeConn()
	}

	return ChatResponse{}, fmt.Errorf("failed after reconnect attempt")
}
