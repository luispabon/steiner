package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// openaiWire speaks the OpenAI chat-completions format. It also backs the
// OpenAI-compatible provider types (ollama, lmstudio, openrouter, litellm),
// which differ only in error-body semantics; providerType carries that
// distinction so retry refinement stays per-instance.
type openaiWire struct {
	baseURL      *url.URL
	apiKey       string
	headers      map[string]string
	model        string
	providerType string
}

func (w *openaiWire) Payload(request ChatRequest, stream bool) ([]byte, error) {
	wire, err := chatRequestWire(request, w.model, stream)
	if err != nil {
		return nil, err
	}
	// prompt_cache_key is only emitted for native OpenAI: ollama and lmstudio
	// may reject unknown top-level fields, and openai_compat/openrouter/litellm
	// backends have no verified support for it.
	if w.providerType == "openai" {
		wire.PromptCacheKey = request.PromptCacheKey
	}
	if shouldDisableRemoteStorage(w.baseURL) {
		wire.Store = boolValuePtr(false)
	}
	return json.Marshal(wire)
}

func (w *openaiWire) HTTPRequest(ctx context.Context, _ ChatRequest, body []byte, stream bool) (*http.Request, error) {
	return buildJSONPostRequest(ctx, w.chatCompletionsURL(), body, stream, w.apiKey, w.headers)
}

func (w *openaiWire) DecodeResponse(resp *http.Response) (ChatResponse, error) {
	var payload openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	return normalizeChatResponse(&payload)
}

func (w *openaiWire) DecodeStream(ctx context.Context, body io.Reader, emit func(ChatChunk) error) error {
	return decodeChatStreamWithHandler(ctx, body, emit)
}

// RefineRetry applies litellm's 429 body semantics, which the engine cannot
// interpret: litellm returns 429 for both rate limits and budget exhaustion, and
// only the latter is permanent. It also relays upstream rate limits as
// "Try again in N seconds" text instead of forwarding the Retry-After header.
func (w *openaiWire) RefineRetry(err error, decision retryDecision) retryDecision {
	if w.providerType != "litellm" || !decision.retry {
		return decision
	}
	httpErr := asHTTPError(err)
	if httpErr == nil || httpErr.StatusCode != http.StatusTooManyRequests {
		return decision
	}
	if isLiteLLMBudgetExceeded(httpErr.Body) {
		return retryDecision{}
	}
	if decision.retryAfter <= 0 {
		if parsed, ok := parseLiteLLMRetryAfter(httpErr.Body); ok {
			decision.retryAfter = parsed
		}
	}
	return decision
}

func (w *openaiWire) chatCompletionsURL() string {
	base := *w.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	return base.String()
}
