package provider

import (
	"context"
	"net/http"
)

// probeSource answers ContextWindow and MaxOutputTokens from a live provider
// probe (e.g. Ollama's /api/show). It is best-effort: any failure or
// unsupported provider type leaves the fields unknown, with no sourceErr.
type probeSource struct {
	httpClient *http.Client
}

func (probeSource) name() FactSource { return FactSourceDiscovery }

func (s probeSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	if want&(fieldSet(fieldContextWindow)|fieldSet(fieldMaxOutput)) == 0 {
		return sourceResult{}
	}
	discoverer := NewDiscoverer(ref.Provider, s.httpClient)
	if discoverer == nil {
		return sourceResult{}
	}
	// Deliberately derived from context.Background(), not the caller's ctx:
	// probes stay decoupled from the caller's cancellation, matching the
	// pre-fact-resolver discovery behavior.
	probeCtx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()
	meta, err := discoverer.DiscoverModelMetadata(probeCtx, ref.BackendModelID)
	if err != nil {
		return sourceResult{}
	}

	var facts ModelFacts
	if want&fieldSet(fieldContextWindow) != 0 && meta.ContextWindow > 0 {
		facts.ContextWindow = Fact[int]{Value: meta.ContextWindow, Known: true, Source: FactSourceDiscovery, Confidence: "medium"}
	}
	if want&fieldSet(fieldMaxOutput) != 0 && meta.MaxOutputTokens > 0 {
		facts.MaxOutputTokens = Fact[int]{Value: meta.MaxOutputTokens, Known: true, Source: FactSourceDiscovery, Confidence: "medium"}
	}
	return sourceResult{facts: facts}
}
