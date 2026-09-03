package modelcatalog

import (
	"fmt"
	"net/url"
	"strings"
)

func joinModelsURL(providerType, baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("parse base URL: missing scheme or host")
	}

	path := strings.TrimRight(u.Path, "/")
	hasV1 := strings.HasSuffix(path, "/v1")
	switch providerType {
	case "openai", "openai_compat", "litellm", "opencode_go", "opencode_zen":
		if hasV1 {
			path = appendURLPath(path, "/models")
		} else {
			path = appendURLPath(path, "/v1/models")
		}
	case "openrouter":
		if strings.HasSuffix(path, "/api/v1") {
			path = appendURLPath(path, "/models")
		} else {
			path = appendURLPath(path, "/api/v1/models")
		}
	case "anthropic":
		if hasV1 {
			path = appendURLPath(path, "/models")
		} else {
			path = appendURLPath(path, "/v1/models")
		}
	case "ollama":
		if hasV1 {
			path = strings.TrimSuffix(path, "/v1")
		}
		path = appendURLPath(path, "/api/tags")
	case "lmstudio":
		if hasV1 {
			path = strings.TrimSuffix(path, "/v1")
		}
		path = appendURLPath(path, "/api/v1/models")
	default:
		return "", fmt.Errorf("join models URL: unsupported provider type %q", providerType)
	}
	u.Path = path
	u.RawPath = ""
	return u.String(), nil
}

func appendURLPath(path, suffix string) string {
	if path == "" {
		return suffix
	}
	return path + suffix
}
