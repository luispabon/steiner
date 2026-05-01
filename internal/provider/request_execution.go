package provider

import (
	"bytes"
	"context"
	"net/http"
	"strings"
)

type requestExecutionInput struct {
	request ChatRequest
	stream  bool
}

func (p *OpenAICompat) executeRequest(ctx context.Context, in requestExecutionInput) (*http.Response, error) {
	body, err := p.marshalRequest(in.request, in.stream)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if in.stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
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
