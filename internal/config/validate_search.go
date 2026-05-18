package config

import (
	"fmt"
	"os"
)

func validateSearchConfig(problems *[]string, cfg SearchConfig) {
	if cfg.Backend == "" {
		return
	}

	validBackends := map[string]bool{
		"google":  true,
		"kagi":    true,
		"brave":   true,
		"searxng": true,
	}
	if !validBackends[cfg.Backend] {
		*problems = append(*problems, fmt.Sprintf("search.backend %q is not supported", cfg.Backend))
		return
	}

	switch cfg.Backend {
	case "google":
		if _, ok := os.LookupEnv("GOOGLE_SEARCH_CX"); !ok {
			*problems = append(*problems, "search.backend is \"google\" but GOOGLE_SEARCH_CX env var is not set")
		}
		if _, ok := os.LookupEnv("GOOGLE_SEARCH_API_KEY"); !ok {
			*problems = append(*problems, "search.backend is \"google\" but GOOGLE_SEARCH_API_KEY env var is not set")
		}
	case "kagi":
		if _, ok := os.LookupEnv("KAGI_API_KEY"); !ok {
			*problems = append(*problems, "search.backend is \"kagi\" but KAGI_API_KEY env var is not set")
		}
	case "brave":
		if _, ok := os.LookupEnv("BRAVE_API_KEY"); !ok {
			*problems = append(*problems, "search.backend is \"brave\" but BRAVE_API_KEY env var is not set")
		}
	case "searxng":
		if cfg.SearxngURL == "" {
			*problems = append(*problems, "search.backend is \"searxng\" but search.searxng_url is not set")
		}
	}
}
