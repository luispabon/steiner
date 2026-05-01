package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type requestExecutionInput struct {
	request ChatRequest
	stream  bool
}

func (p *OpenAICompat) executeRequest(ctx context.Context, in requestExecutionInput) (*http.Response, error) {
	payload, err := p.buildRequestPayload(in.request, in.stream)
	if err != nil {
		return nil, err
	}
	req, err := p.buildHTTPRequest(ctx, payload, in.stream)
	if err != nil {
		return nil, err
	}
	return p.executeHTTP(ctx, req)
}

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
	return req, nil
}

func (p *OpenAICompat) executeHTTP(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, p.readErrorResponse(resp)
	}
	return resp, nil
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
