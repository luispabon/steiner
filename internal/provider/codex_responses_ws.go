package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

type codexWSProvider struct {
	apiKey          string
	headers         map[string]string
	model           string
	echoTurnState   bool
	fallbackEnabled bool
	fallback        Provider
	wsURL           string
	mu              sync.Mutex
	conn            *websocket.Conn
	dialCacheKey    string
}

func newCodexResponsesWSWithEcho(cfg ClientConfig, fallbackEnabled, echoTurnState bool) (Provider, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	provider := &codexWSProvider{
		apiKey:          cfg.APIKey,
		headers:         copyHeaders(cfg.Headers),
		model:           cfg.Model,
		echoTurnState:   echoTurnState,
		fallbackEnabled: fallbackEnabled,
		wsURL:           WSEndpointURL,
	}

	if fallbackEnabled {
		fallback, err := NewCodexResponses(cfg)
		if err != nil {
			return nil, err
		}
		provider.fallback = fallback
	}

	return provider, nil
}

func newCodexResponsesWS(cfg ClientConfig, fallbackEnabled bool) (Provider, error) {
	return newCodexResponsesWSWithEcho(cfg, fallbackEnabled, false)
}

// NewCodexResponsesWS constructs a Codex WebSocket provider with automatic HTTP fallback enabled.
func NewCodexResponsesWS(cfg ClientConfig) (Provider, error) {
	return newCodexResponsesWS(cfg, true)
}

// NewCodexResponsesWSNoFallback constructs a Codex WebSocket provider with no HTTP fallback; WebSocket failures return an error instead of degrading.
func NewCodexResponsesWSNoFallback(cfg ClientConfig) (Provider, error) {
	return newCodexResponsesWS(cfg, false)
}

func (p *codexWSProvider) SupportsUsageStats() bool {
	return true
}

func (p *codexWSProvider) ChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := p.executeRequest(ctx, request)
	if err == nil {
		return result, nil
	}

	if !p.fallbackEnabled {
		return ChatResponse{}, err
	}

	return p.fallback.ChatCompletion(ctx, request)
}

func (p *codexWSProvider) StreamChatCompletion(ctx context.Context, request ChatRequest) (<-chan ChatChunk, error) {
	out := make(chan ChatChunk, 1)
	go func() {
		defer close(out)

		p.mu.Lock()
		result, err := p.executeRequest(ctx, request)
		p.mu.Unlock()

		if err == nil {
			select {
			case out <- ChatChunk{
				Delta:        result.Message,
				Usage:        result.Usage,
				Done:         true,
				FinishReason: result.FinishReason,
			}:
			case <-ctx.Done():
			}
			return
		}

		if !p.fallbackEnabled {
			select {
			case out <- ChatChunk{Done: true, Error: err.Error(), OriginalError: err}:
			case <-ctx.Done():
			}
			return
		}

		reason := err.Error()
		select {
		case out <- ChatChunk{
			Diagnostic: fmt.Sprintf("Codex WebSocket unavailable, falling back to HTTP: %s", reason),
			Severity:   "warning",
		}:
		default:
		}

		if err := ctx.Err(); err != nil {
			return
		}

		fallbackStream, fallbackErr := p.fallback.StreamChatCompletion(ctx, request)
		if fallbackErr != nil {
			select {
			case out <- ChatChunk{Done: true, Error: fallbackErr.Error(), OriginalError: fallbackErr}:
			case <-ctx.Done():
			}
			return
		}

		for chunk := range fallbackStream {
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

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

	p.conn = conn
	return nil
}

func (p *codexWSProvider) executeRequest(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	var reconnectAttempt bool

	for attempts := 0; attempts < 2; attempts++ {
		if err := p.ensureConnection(ctx, request); err != nil {
			if reconnectAttempt {
				return ChatResponse{}, fmt.Errorf("reconnect failed: %w", err)
			}
			reconnectAttempt = true
			p.conn = nil
			continue
		}

		result, turnState, err := p.sendRequest(ctx, request)
		if err != nil {
			if reconnectAttempt {
				return ChatResponse{}, err
			}
			reconnectAttempt = true
			_ = p.conn.CloseNow()
			p.conn = nil
			continue
		}

		_ = turnState
		return result, nil
	}

	return ChatResponse{}, fmt.Errorf("failed after reconnect attempt")
}

func buildWSRequestFrame(request ChatRequest, model string, turnState string) ([]byte, error) {
	wire, err := responsesRequestWire(request, model, false)
	if err != nil {
		return nil, err
	}
	wire.PromptCacheKey = request.PromptCacheKey

	frame := responsesRequestMap(wire)
	delete(frame, "model")
	frame["type"] = "response.create"
	frame["model"] = model

	if turnState != "" {
		frame["client_metadata"] = map[string]any{
			WSClientMetadataTurnStateKey: turnState,
		}
	}

	return json.Marshal(frame)
}

func (p *codexWSProvider) sendRequest(ctx context.Context, request ChatRequest) (ChatResponse, string, error) {
	payload, err := buildWSRequestFrame(request, p.model, "")
	if err != nil {
		return ChatResponse{}, "", fmt.Errorf("build frame: %w", err)
	}

	if err := p.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return ChatResponse{}, "", fmt.Errorf("write frame: %w", err)
	}

	state := responsesStreamState{}
	var capturedTurnState string

	for {
		typ, data, err := p.conn.Read(ctx)
		if err != nil {
			return ChatResponse{}, "", fmt.Errorf("read response: %w", err)
		}

		if typ != websocket.MessageText {
			continue
		}

		event := string(data)

		if p.echoTurnState && capturedTurnState == "" {
			captureWSEventTurnState(event, &capturedTurnState)
		}

		done, err := processResponsesStreamEvent(&state, event, func(_ ChatChunk) error {
			return nil
		})
		if err != nil {
			return ChatResponse{}, "", fmt.Errorf("process event: %w", err)
		}

		if done {
			break
		}
	}

	var finalChunk ChatChunk
	flushResponsesStreamStateInto(&finalChunk, state)

	hadUsableFinalChunk := state.sawDone || state.finishReason != ""
	if !hadUsableFinalChunk {
		return ChatResponse{}, "", fmt.Errorf("stream completed without a final chunk")
	}

	resp := ChatResponse{
		Message:      finalChunk.Delta,
		Usage:        finalChunk.Usage,
		FinishReason: finalChunk.FinishReason,
	}

	return resp, capturedTurnState, nil
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

func captureWSEventTurnState(event string, dest *string) {
	if *dest != "" {
		return
	}

	var evt map[string]any
	if err := json.Unmarshal([]byte(event), &evt); err != nil {
		return
	}

	if eventType, ok := evt["type"].(string); ok && eventType == WSEventTypeMetadata {
		if headers, ok := evt["headers"].(map[string]any); ok {
			if ts, ok := headers[WSHeaderTurnState].(string); ok && ts != "" {
				*dest = ts
			}
		}
	}
}

func flushResponsesStreamStateInto(chunk *ChatChunk, state responsesStreamState) {
	*chunk = responsesStreamStateToChatChunk(state)
}
