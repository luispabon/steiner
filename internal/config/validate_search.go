package config

import (
	"fmt"
)

func validateSearchConfig(problems *[]string, cfg SearchConfig) {
	if cfg.Backend == "" {
		return
	}

	validBackends := map[string]bool{
		"kagi":    true,
		"brave":   true,
		"searxng": true,
	}
	if !validBackends[cfg.Backend] {
		*problems = append(*problems, fmt.Sprintf("search.backend %q is not supported", cfg.Backend))
		return
	}

	switch cfg.Backend {
	case "kagi":
		if cfg.KagiAPIKey == "" {
			*problems = append(*problems, "search.backend is \"kagi\" but kagi_api_key is not set")
		}
	case "brave":
		if cfg.BraveAPIKey == "" {
			*problems = append(*problems, "search.backend is \"brave\" but brave_api_key is not set")
		}
	case "searxng":
		if cfg.SearxngURL == "" {
			*problems = append(*problems, "search.backend is \"searxng\" but search.searxng_url is not set")
		}
	}
}
