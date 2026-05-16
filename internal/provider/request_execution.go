package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (p *OpenAICompat) buildRequestPayload(request ChatRequest, stream bool) ([]byte, error) {
	return p.marshalRequest(request, stream)
}

func (p *OpenAICompat) buildHTTPRequest(ctx context.Context, body []byte, stream bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func (p *OpenAICompat) executeHTTP(_ context.Context, req *http.Request) (*http.Response, error) {
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer closeResponseBody(resp.Body)
		return nil, p.readErrorResponse(resp)
	}
	return resp, nil
}

func (p *OpenAICompat) decodeNonStreamResponse(resp *http.Response) (*openAIResponse, error) {
	var payload openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode chat completion response: %w", err)
	}
	return &payload, nil
}

func (p *OpenAICompat) decodeStreamResponse(ctx context.Context, body io.Reader, out chan<- ChatChunk) error {
	return decodeChatStream(ctx, body, out)
}

func closeResponseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_ = body.Close()
}

func (p *OpenAICompat) readErrorResponse(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return fmt.Errorf("read error response body: %w", err)
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       strings.TrimSpace(string(body)),
		Header:     resp.Header.Clone(),
	}
}

func (p *OpenAICompat) chatCompletionsURL() string {
	base := *p.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	return base.String()
}

func (p *OpenAICompat) marshalRequest(request ChatRequest, stream bool) ([]byte, error) {
	wire, err := chatRequestWire(request, p.model, stream)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}
