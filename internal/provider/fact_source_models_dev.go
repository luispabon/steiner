package provider

import (
	"context"
	"sync"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// modelsDevLoader lazily loads and parses the models.dev cache at most once,
// shared across every modelsDevSource instance constructed from it (so a
// single resolution — or a whole ResolveReasoningBatch pass over many
// aliases — touches the cache file/network at most once, and never at all
// when no consulted source needs models.dev data).
type modelsDevLoader struct {
	cache *metadata.Cache
	once  sync.Once
	index *metadata.Index
	err   string // LoadStatus.Reason if the load degraded; "" if clean
}

// newModelsDevLoader returns a modelsDevLoader that loads from cache on
// first use.
func newModelsDevLoader(cache *metadata.Cache) *modelsDevLoader {
	return &modelsDevLoader{cache: cache}
}

func (l *modelsDevLoader) load(_ context.Context) (*metadata.Index, string) {
	l.once.Do(func() {
		loadCtx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
		defer cancel()
		result := l.cache.LoadBestEffortWithStatus(loadCtx)
		if result.Data != nil {
			l.index = metadata.ParseIndex(result.Data)
		}
		l.err = result.Status.Reason
	})
	return l.index, l.err
}

// modelsDevSource answers facts from a lazily-loaded models.dev index. The
// loader is shared with sibling modelsDevSource instances built from the
// same newModelsDevLoader call, so the cache is loaded at most once no
// matter how many resolutions consult it.
type modelsDevSource struct {
	loader *modelsDevLoader
}

func (modelsDevSource) name() FactSource { return FactSourceModelsDev }

func (s modelsDevSource) resolve(ctx context.Context, ref modelRef, want fieldSet) sourceResult {
	if want == 0 {
		return sourceResult{}
	}

	idx, loadErr := s.loader.load(ctx)
	if idx == nil {
		if loadErr != "" {
			return sourceResult{sourceErr: "models.dev unavailable: " + loadErr}
		}
		return sourceResult{}
	}
	// idx may be non-nil with loadErr also non-empty (stale-cache-but-usable,
	// matching stage B2's documented behavior) — consult it regardless, exactly
	// as before.

	providerID := ref.Profile.ModelsDevID
	if providerID == "" {
		providerID = ref.ProviderAlias
	}
	lookup := idx.LookupProvider(providerID, ref.BackendModelID)
	info := lookup.Info

	notes := modelsDevDegradationNotes(lookup.Reason, want)
	facts := ModelFacts{
		ContextWindow:     modelsDevContextWindowFact(want, info),
		MaxOutputTokens:   modelsDevMaxOutputFact(want, info),
		Vision:            modelsDevVisionFact(want, info),
		ReasoningEfforts:  modelsDevEffortsFact(want, info),
		ReasoningEchoBack: modelsDevEchoBackFact(want, info),
		Transport:         modelsDevTransportFact(want, ref, info),
	}

	return sourceResult{facts: facts, notes: notes}
}

// modelsDevDegradationNotes attaches reason to every wanted field when the
// lookup could not safely return a match.
func modelsDevDegradationNotes(reason string, want fieldSet) map[factField]string {
	notes := map[factField]string{}
	if reason != metadata.LookupReasonMalformed && reason != metadata.LookupReasonProviderMismatch {
		return notes
	}
	for _, f := range []factField{fieldContextWindow, fieldMaxOutput, fieldVision, fieldEfforts, fieldEchoBack, fieldTransport} {
		if want&fieldSet(f) != 0 {
			notes[f] = reason
		}
	}
	return notes
}

func modelsDevContextWindowFact(want fieldSet, info metadata.ModelInfo) Fact[int] {
	if want&fieldSet(fieldContextWindow) == 0 || info.ContextWindow <= 0 {
		return Fact[int]{}
	}
	return Fact[int]{Value: info.ContextWindow, Known: true, Source: FactSourceModelsDev, Confidence: "medium"}
}

func modelsDevMaxOutputFact(want fieldSet, info metadata.ModelInfo) Fact[int] {
	if want&fieldSet(fieldMaxOutput) == 0 || info.MaxOutputTokens <= 0 {
		return Fact[int]{}
	}
	return Fact[int]{Value: info.MaxOutputTokens, Known: true, Source: FactSourceModelsDev, Confidence: "medium"}
}

func modelsDevVisionFact(want fieldSet, info metadata.ModelInfo) Fact[bool] {
	if want&fieldSet(fieldVision) == 0 || !info.Found {
		return Fact[bool]{}
	}
	return Fact[bool]{Value: info.VisionInput, Known: true, Source: FactSourceModelsDev, Confidence: "medium"}
}

func modelsDevEffortsFact(want fieldSet, info metadata.ModelInfo) Fact[[]string] {
	if want&fieldSet(fieldEfforts) == 0 || len(info.ReasoningSupportedEfforts) == 0 {
		return Fact[[]string]{}
	}
	return Fact[[]string]{Value: info.ReasoningSupportedEfforts, Known: true, Source: FactSourceModelsDev, Confidence: "medium"}
}

func modelsDevEchoBackFact(want fieldSet, info metadata.ModelInfo) Fact[bool] {
	if want&fieldSet(fieldEchoBack) == 0 || !info.Found {
		return Fact[bool]{}
	}
	return Fact[bool]{Value: info.ReasoningEchoBack, Known: true, Source: FactSourceModelsDev, Confidence: "medium"}
}

func modelsDevTransportFact(want fieldSet, ref modelRef, info metadata.ModelInfo) Fact[transportChoice] {
	if want&fieldSet(fieldTransport) == 0 {
		return Fact[transportChoice]{}
	}
	tt := metadataProviderTransport(info)
	if tt == TransportConfigured {
		return Fact[transportChoice]{}
	}
	var pt config.ProviderType
	switch tt {
	case TransportAnthropic:
		pt = config.ProviderTypeAnthropic
	case TransportOpenAICompat:
		pt = config.ProviderTypeOpenAICompat
	}
	return Fact[transportChoice]{
		Value: transportChoice{
			ProviderType: pt, Transport: tt,
			Reason: "models.dev provider override for " + ref.BackendModelID,
		},
		Known: true, Source: FactSourceModelsDev, Confidence: "medium",
	}
}
