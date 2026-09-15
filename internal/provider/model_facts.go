package provider

// FactSource identifies where a resolved model fact came from.
type FactSource string

const (
	// FactSourceConfig means the fact came directly from user config.
	FactSourceConfig FactSource = "config"
	// FactSourceCatalog means the fact came from the provider model catalog
	// cache. Wired in a later stage.
	FactSourceCatalog FactSource = "catalog"
	// FactSourceDiscovery means the fact came from a live provider probe.
	// Wired in a later stage.
	FactSourceDiscovery FactSource = "discovery"
	// FactSourceModelsDev means the fact came from models.dev metadata.
	// Wired in a later stage.
	FactSourceModelsDev FactSource = "models.dev"
	// FactSourceFallback means the fact came from a built-in family table or
	// a conservative default.
	FactSourceFallback FactSource = "fallback"
	// FactSourceUnknown means no source could answer the fact.
	FactSourceUnknown FactSource = "unknown"
)

// Fact is one resolved model fact plus its provenance.
type Fact[T any] struct {
	Value      T
	Known      bool
	Source     FactSource
	Confidence string
	Note       string
}

// ModelFacts is the full per-fact resolution result for a model.
type ModelFacts struct {
	ContextWindow     Fact[int]
	MaxOutputTokens   Fact[int]
	Vision            Fact[bool]
	ReasoningEfforts  Fact[[]string]
	ReasoningEchoBack Fact[bool]
	Transport         Fact[transportChoice]
}

// applyFacts populates rm's legacy exported fields from rm.Facts. It is the
// single place ModelFacts gets projected onto ResolvedModel's stable API.
//
// For the non-discovery path (useDiscovery=false), rm's legacy fields are
// left exactly as resolveReference's base construction set them; only
// rm.Facts is populated, preserving Resolve's pinned behavior.
func applyFacts(rm *ResolvedModel, facts ModelFacts, useDiscovery bool) {
	rm.Facts = facts
	if !useDiscovery {
		return
	}

	rm.EffectiveLimits = deriveEffectiveLimits(facts.ContextWindow.Value, facts.MaxOutputTokens.Value)
	rm.MetadataSource, rm.Confidence = deriveMetadataSourceAndConfidence(facts)

	if facts.Vision.Known {
		v := facts.Vision.Value
		rm.Vision = &v
	}
	rm.Reasoning = ReasoningCapabilities{
		SupportedEfforts:      facts.ReasoningEfforts.Value,
		ProviderDefaultEffort: rm.Reasoning.ProviderDefaultEffort,
		Source:                string(facts.ReasoningEfforts.Source),
		Confidence:            facts.ReasoningEfforts.Confidence,
	}
	if !facts.ReasoningEfforts.Known {
		rm.Reasoning.Source = "unknown"
		rm.Reasoning.Confidence = "unknown"
	}
	rm.ReasoningEchoBack = facts.ReasoningEchoBack.Known && facts.ReasoningEchoBack.Value

	rm.EffectiveProviderType = facts.Transport.Value.ProviderType
	rm.EffectiveTransport = facts.Transport.Value.Transport
	rm.TransportOverrideReason = facts.Transport.Value.Reason
}

// deriveMetadataSourceAndConfidence derives the legacy MetadataSource and
// Confidence fields (which historically describe limits provenance only)
// from the resolved ContextWindow and MaxOutputTokens facts.
func deriveMetadataSourceAndConfidence(facts ModelFacts) (source, confidence string) {
	if facts.ContextWindow.Source == FactSourceConfig && facts.MaxOutputTokens.Source == FactSourceConfig {
		return string(FactSourceConfig), "high"
	}
	if facts.ContextWindow.Source != FactSourceConfig {
		return string(facts.ContextWindow.Source), facts.ContextWindow.Confidence
	}
	return string(facts.MaxOutputTokens.Source), facts.MaxOutputTokens.Confidence
}
