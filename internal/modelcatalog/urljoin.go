package modelcatalog

import (
	"fmt"
	"net/url"
	"strings"
)

func joinModelsURL(providerType, baseURL string) (string, error) {
	switch providerType {
	case "openrouter":
		return joinPath(baseURL, func(path string, hasV1 bool) string {
			if strings.HasSuffix(path, "/api/v1") {
				return appendURLPath(path, "/models")
			}
			return appendURLPath(path, "/api/v1/models")
		})
	case "anthropic":
		return joinOpenAIStyleModelsURL(baseURL)
	case "ollama":
		return joinPath(baseURL, func(path string, hasV1 bool) string {
			if hasV1 {
				path = strings.TrimSuffix(path, "/v1")
			}
			return appendURLPath(path, "/api/tags")
		})
	case "lmstudio":
		return joinPath(baseURL, func(path string, hasV1 bool) string {
			if hasV1 {
				path = strings.TrimSuffix(path, "/v1")
			}
			return appendURLPath(path, "/api/v1/models")
		})
	default:
		return "", fmt.Errorf("join models URL: unsupported provider type %q", providerType)
	}
}

// joinOpenAIStyleModelsURL builds a /v1/models URL (or /models, if baseURL
// already ends in /v1). Every provider type routed to OpenAIEnumerator
// (openai, openai_compat, litellm, opencode_go, opencode_zen, ...) shares
// this exact shape, so OpenAIEnumerator calls this directly instead of going
// through joinModelsURL's provider-type switch — that switch only needs to
// enumerate the handful of genuinely distinct URL shapes (ollama, lmstudio,
// openrouter, and anthropic, which happens to share this same shape too).
func joinOpenAIStyleModelsURL(baseURL string) (string, error) {
	return joinPath(baseURL, func(path string, hasV1 bool) string {
		if hasV1 {
			return appendURLPath(path, "/models")
		}
		return appendURLPath(path, "/v1/models")
	})
}

func joinPath(baseURL string, build func(path string, hasV1 bool) string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("parse base URL: missing scheme or host")
	}
	path := strings.TrimRight(u.Path, "/")
	hasV1 := strings.HasSuffix(path, "/v1")
	u.Path = build(path, hasV1)
	u.RawPath = ""
	return u.String(), nil
}

func appendURLPath(path, suffix string) string {
	if path == "" {
		return suffix
	}
	return path + suffix
}
