package provider

import (
	"context"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// modelsDevSource answers facts from already-loaded models.dev cache data.
// The caller is responsible for loading the cache once (via
// metadata.Cache.LoadBestEffortWithStatus) and passing the result in.
type modelsDevSource struct {
	data       []byte // nil if load failed
	loadReason string // non-empty describes why data is nil or degraded
}

func (modelsDevSource) name() FactSource { return FactSourceModelsDev }

func (s modelsDevSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	if s.data == nil {
		if s.loadReason != "" {
			return sourceResult{sourceErr: "models.dev unavailable: " + s.loadReason}
		}
		return sourceResult{}
	}
	// A non-empty loadReason alongside non-nil data means a refresh attempt
	// degraded (e.g. offline) but a stale cache is still usable: consult it
	// rather than reporting a source error, matching the pre-fact-resolver
	// stale-cache-fallback behavior.

	providerID := ref.Profile.ModelsDevID
	if providerID == "" {
		providerID = ref.ProviderAlias
	}
	lookup := metadata.LookupWithProviderResult(s.data, providerID, ref.BackendModelID)
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
	if reason != metadata.LookupReasonMalformed && reason != metadata.LookupReasonProviderMismatch && reason != metadata.LookupReasonConflict {
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
