package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// anthropicWire speaks the Anthropic Messages format.
type anthropicWire struct {
	baseURL *url.URL
	apiKey  string
	headers map[string]string
	model   string
}

func (w *anthropicWire) Payload(request ChatRequest, stream bool) ([]byte, error) {
	return json.Marshal(anthropicRequestWire(request, w.model, stream))
}

func (w *anthropicWire) HTTPRequest(ctx context.Context, chat ChatRequest, body []byte, stream bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if strings.TrimSpace(w.apiKey) != "" {
		req.Header.Set("x-api-key", w.apiKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	headers := prepareTransportSessionHeaders(w.headers, chat)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func (w *anthropicWire) DecodeResponse(resp *http.Response) (ChatResponse, error) {
	var payload anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	response, err := normalizeAnthropicChatResponse(&payload)
	if err != nil {
		return ChatResponse{}, err
	}
	response.UpstreamEndpoint = resp.Header.Get("X-Opencode-Endpoint-Id")
	return response, nil
}

func (w *anthropicWire) DecodeStream(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error {
	return decodeAnthropicStreamWithHandler(ctx, body, emit)
}

func (w *anthropicWire) messagesURL() string {
	base := *w.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/messages"
	return base.String()
}
