package provider

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// modelsDevLoader lazily loads and parses the models.dev cache at most once,
// shared across every modelsDevSource instance constructed from it (so a
// single resolution — or a whole session sharing one Resolver across many
// aliases — touches the cache file/network at most once, and never at all
// when no consulted source needs models.dev data).
type modelsDevLoader struct {
	cache    *metadata.Cache
	loadFunc func(context.Context) metadata.LoadResult // optional override; nil uses cache.LoadBestEffortWithStatus
	once     sync.Once
	index    *metadata.Index
	err      string // LoadStatus.Reason if the load degraded; "" if clean
}

// newModelsDevLoader returns a modelsDevLoader that loads from cache on
// first use.
func newModelsDevLoader(cache *metadata.Cache) *modelsDevLoader {
	return &modelsDevLoader{cache: cache}
}

// newModelsDevLoaderWithFunc returns a modelsDevLoader that loads via fn
// instead of a metadata.Cache, for tests that need to observe or control
// exactly when/how many times the underlying load happens.
func newModelsDevLoaderWithFunc(fn func(context.Context) metadata.LoadResult) *modelsDevLoader {
	return &modelsDevLoader{loadFunc: fn}
}

func (l *modelsDevLoader) load(ctx context.Context) (*metadata.Index, string) {
	l.once.Do(func() {
		var result metadata.LoadResult
		if l.loadFunc != nil {
			result = l.loadFunc(ctx)
		} else {
			loadCtx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
			defer cancel()
			result = l.cache.LoadBestEffortWithStatus(loadCtx)
		}
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
	if idx != nil && idx.Malformed() {
		return sourceResult{sourceErr: "models.dev unavailable: malformed metadata"}
	}
	if idx == nil {
		if loadErr != "" {
			return sourceResult{sourceErr: "models.dev unavailable: " + loadErr}
		}
		return sourceResult{}
	}
	// idx may be non-nil with loadErr also non-empty (stale-cache-but-usable,
	// matching stage B2's documented behavior) — consult it regardless, exactly
	// as before.

	lookup, merged := modelsDevLookup(idx, ref)
	info := lookup.Info

	confidence := "medium"
	note := ""
	if merged {
		confidence = "low"
		note = fmt.Sprintf("models.dev: merged %d providers", info.ProviderCount)
	}

	notes := modelsDevDegradationNotes(lookup.Reason, want)
	facts := ModelFacts{
		ContextWindow:     modelsDevContextWindowFact(want, info, confidence, note),
		MaxOutputTokens:   modelsDevMaxOutputFact(want, info, confidence, note),
		Vision:            modelsDevVisionFact(want, info, confidence, note),
		ReasoningEfforts:  modelsDevEffortsFact(want, info, confidence, note),
		ReasoningEchoBack: modelsDevEchoBackFact(want, info, confidence, note),
	}
	if !merged {
		facts.Transport = modelsDevTransportFact(want, ref, info)
	}

	return sourceResult{facts: facts, notes: notes}
}

// modelsDevLookup resolves modelID for ref, choosing between a strict
// provider lookup and a cross-provider merge. Named (non-generic) profiles
// always use a strict lookup against their fixed ModelsDevID. Generic
// profiles (openai_compat, ollama, litellm) first try a strict lookup using
// their configured alias as the models.dev provider ID, for users who name
// aliases after real models.dev provider keys; if that lookup finds nothing
// under that key (not_found or provider_mismatch), it falls back to a merge
// across every provider that lists the model. A malformed strict result is
// never overridden by a merge — malformed stays malformed. merged reports
// whether the returned LookupResult came from the merge fallback.
func modelsDevLookup(idx *metadata.Index, ref modelRef) (result metadata.LookupResult, merged bool) {
	if !ref.Profile.Generic {
		return idx.LookupProvider(ref.Profile.ModelsDevID, ref.BackendModelID), false
	}
	strict := idx.LookupProvider(ref.ProviderAlias, ref.BackendModelID)
	if strict.Reason != metadata.LookupReasonNotFound && strict.Reason != metadata.LookupReasonProviderMismatch {
		return strict, false
	}
	return idx.LookupMerged(ref.BackendModelID), true
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

func modelsDevContextWindowFact(want fieldSet, info metadata.ModelInfo, confidence, note string) Fact[int] {
	if want&fieldSet(fieldContextWindow) == 0 || info.ContextWindow <= 0 {
		return Fact[int]{}
	}
	return Fact[int]{Value: info.ContextWindow, Known: true, Source: FactSourceModelsDev, Confidence: confidence, Note: note}
}

func modelsDevMaxOutputFact(want fieldSet, info metadata.ModelInfo, confidence, note string) Fact[int] {
	if want&fieldSet(fieldMaxOutput) == 0 || info.MaxOutputTokens <= 0 {
		return Fact[int]{}
	}
	return Fact[int]{Value: info.MaxOutputTokens, Known: true, Source: FactSourceModelsDev, Confidence: confidence, Note: note}
}

func modelsDevVisionFact(want fieldSet, info metadata.ModelInfo, confidence, note string) Fact[bool] {
	if want&fieldSet(fieldVision) == 0 || !info.Found {
		return Fact[bool]{}
	}
	return Fact[bool]{Value: info.VisionInput, Known: true, Source: FactSourceModelsDev, Confidence: confidence, Note: note}
}

func modelsDevEffortsFact(want fieldSet, info metadata.ModelInfo, confidence, note string) Fact[[]string] {
	if want&fieldSet(fieldEfforts) == 0 || len(info.ReasoningSupportedEfforts) == 0 {
		return Fact[[]string]{}
	}
	return Fact[[]string]{Value: slices.Clone(info.ReasoningSupportedEfforts), Known: true, Source: FactSourceModelsDev, Confidence: confidence, Note: note}
}

func modelsDevEchoBackFact(want fieldSet, info metadata.ModelInfo, confidence, note string) Fact[bool] {
	if want&fieldSet(fieldEchoBack) == 0 || !info.Found {
		return Fact[bool]{}
	}
	return Fact[bool]{Value: info.ReasoningEchoBack, Known: true, Source: FactSourceModelsDev, Confidence: confidence, Note: note}
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
