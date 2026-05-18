package config

func applySearchConfigPatch(cfg *Config, patch configPatch) {
	if patch.Search != nil {
		applySearchPatch(&cfg.Search, patch.Search)
	}
}

func applySearchPatch(dst *SearchConfig, patch *searchPatch) {
	if patch.Backend != nil {
		dst.Backend = *patch.Backend
	}
	if patch.SearxngURL != nil {
		dst.SearxngURL = *patch.SearxngURL
	}
}
