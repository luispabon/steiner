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
// TODO(stage B2): project remaining legacy fields (MetadataSource,
// Confidence, EffectiveLimits, EffectiveTransport, TransportOverrideReason,
// Vision, Reasoning, ReasoningEchoBack) from facts here for the discovery
// path too, once the discovery path is migrated to the fact resolver.
func applyFacts(rm *ResolvedModel, facts ModelFacts) {
	rm.Facts = facts
}
