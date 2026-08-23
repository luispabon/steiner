package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Enumerator discovers models from one provider wire format.
type Enumerator interface {
	Enumerate(ctx context.Context, ep Endpoint, opts EnumerationOptions) (EnumerationResult, error)
}

// Endpoint describes the configured provider endpoint used for enumeration.
type Endpoint struct {
	Alias   string
	Type    string
	BaseURL string
	APIKey  string
	Headers map[string]string
}

// EnumerationOptions controls conditional enumeration requests.
type EnumerationOptions struct {
	ETag string
}

// EnumerationResult contains models discovered by an Enumerator.
type EnumerationResult struct {
	Models      []DiscoveredModel
	ETag        string
	NotModified bool
}

// ForType returns an Enumerator for a supported provider type.
func ForType(t string) (Enumerator, error) {
	return ForTypeWithClient(t, nil)
}

// ForTypeWithClient returns an Enumerator using client for HTTP requests. A nil
// client uses a new default http.Client.
func ForTypeWithClient(t string, client *http.Client) (Enumerator, error) {
	switch t {
	case "openai", "openai_compat", "litellm":
		return NewOpenAIEnumerator(client), nil
	case "ollama":
		return NewOllamaEnumerator(client), nil
	case "lmstudio":
		return NewLMStudioEnumerator(client), nil
	case "openrouter":
		return NewOpenRouterEnumerator(client), nil
	case "anthropic":
		return NewAnthropicEnumerator(client), nil
	case "codex":
		return NewCodexEnumerator(client, "", nil), nil
	default:
		return nil, fmt.Errorf("unknown enumerator type %q", t)
	}
}

func clientOrDefault(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{}
	}
	return client
}

func newGETRequest(ctx context.Context, ep Endpoint, endpoint string, authorization string, setAuthorization bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create model enumeration request: %w", err)
	}
	for key, value := range ep.Headers {
		req.Header.Set(key, value)
	}
	if setAuthorization {
		req.Header.Set("Authorization", authorization)
	}
	return req, nil
}

func doJSONRequest(client *http.Client, req *http.Request, response any) (string, error) {
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request model enumeration: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("enumerate models: unexpected status code %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(response); err != nil {
		return "", fmt.Errorf("decode model enumeration response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode model enumeration response: unexpected trailing JSON")
		}
		return "", fmt.Errorf("decode model enumeration response: %w", err)
	}
	return resp.Header.Get("ETag"), nil
}
