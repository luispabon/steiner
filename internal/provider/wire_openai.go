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
	// backends have no verified support for it. prompt_cache_retention follows
	// the same gating and is only set for OpenAI models that support extended
	// retention (24h idle, vs. the default 5-10 minutes).
	if w.providerType == "openai" {
		wire.PromptCacheKey = request.PromptCacheKey
		if supportsExtendedCacheRetention(wire.Model) {
			wire.PromptCacheRetention = "24h"
		}
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

// supportsExtendedCacheRetention checks if a model supports 24-hour prompt
// cache retention. Match by EXACT model ID only — do NOT use prefix matching.
// gpt-5.4-mini is a real model that is absent from this list and sending
// prompt_cache_retention for an unsupported model risks a 400 error.
func supportsExtendedCacheRetention(model string) bool {
	switch model {
	case "gpt-5.5", "gpt-5.5-pro", "gpt-5.4", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-chat-latest", "gpt-5", "gpt-5-codex", "gpt-4.1":
		return true
	default:
		return false
	}
}
