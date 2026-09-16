package provider

import "context"

// factPrecedence lists, for each field, the sources allowed to answer it, in
// winning order (first source to answer wins).
//
// fieldTransport's explicit sources are configSource's override and
// providerFixedSource's Codex override (both report FactSourceConfig, see
// providerFixedSource.name doc) followed by modelsDevSource's npm-based
// override. A separate, unconditional default step in resolveFacts (not
// listed here) falls back to the configured provider type when neither
// explicit source answers.
var factPrecedence = map[factField][]FactSource{
	fieldContextWindow: {FactSourceConfig, FactSourceCatalog, FactSourceDiscovery, FactSourceModelsDev, FactSourceFallback},
	fieldMaxOutput:     {FactSourceConfig, FactSourceCatalog, FactSourceDiscovery, FactSourceModelsDev, FactSourceFallback},
	fieldVision:        {FactSourceConfig, FactSourceModelsDev},
	fieldEfforts:       {FactSourceConfig, FactSourceCatalog, FactSourceModelsDev, FactSourceFallback},
	fieldEchoBack:      {FactSourceConfig, FactSourceModelsDev},
	fieldTransport:     {FactSourceConfig, FactSourceModelsDev},
}

// allFields is the full set of resolvable fields.
const allFields fieldSet = fieldSet(fieldContextWindow | fieldMaxOutput | fieldVision | fieldEfforts | fieldEchoBack | fieldTransport)

// factMerge merges one field of a sourceResult into an accumulating
// ModelFacts, reporting whether the field was known and thus merged.
type factMerge struct {
	field factField
	merge func(dst *ModelFacts, src ModelFacts) bool
}

var factMerges = []factMerge{
	{fieldContextWindow, func(dst *ModelFacts, src ModelFacts) bool {
		if !src.ContextWindow.Known {
			return false
		}
		dst.ContextWindow = src.ContextWindow
		return true
	}},
	{fieldMaxOutput, func(dst *ModelFacts, src ModelFacts) bool {
		if !src.MaxOutputTokens.Known {
			return false
		}
		dst.MaxOutputTokens = src.MaxOutputTokens
		return true
	}},
	{fieldVision, func(dst *ModelFacts, src ModelFacts) bool {
		if !src.Vision.Known {
			return false
		}
		dst.Vision = src.Vision
		return true
	}},
	{fieldEfforts, func(dst *ModelFacts, src ModelFacts) bool {
		if !src.ReasoningEfforts.Known {
			return false
		}
		dst.ReasoningEfforts = src.ReasoningEfforts
		return true
	}},
	{fieldEchoBack, func(dst *ModelFacts, src ModelFacts) bool {
		if !src.ReasoningEchoBack.Known {
			return false
		}
		dst.ReasoningEchoBack = src.ReasoningEchoBack
		return true
	}},
	{fieldTransport, func(dst *ModelFacts, src ModelFacts) bool {
		if !src.Transport.Known {
			return false
		}
		dst.Transport = src.Transport
		return true
	}},
}

// fieldsThisSourceMayAnswer returns the bitwise-OR of every factField whose
// precedence list contains source.
func fieldsThisSourceMayAnswer(source FactSource) fieldSet {
	var set fieldSet
	for field, sources := range factPrecedence {
		for _, s := range sources {
			if s == source {
				set |= fieldSet(field)
				break
			}
		}
	}
	return set
}

// resolveFacts resolves ModelFacts for ref by consulting sources in order,
// then applying hardcoded conservative defaults for any fields still
// unresolved. It returns the resolved facts, any degradation notes reported
// by sources for fields they could not answer, and a deduped list of
// source-level errors.
func resolveFacts(ctx context.Context, ref modelRef, sources []factSource) (ModelFacts, map[factField][]string, []string) {
	var facts ModelFacts
	pending := allFields
	var sourceErrs []string
	seenErrs := make(map[string]bool)
	notes := make(map[factField][]string)

	for _, source := range sources {
		want := pending & fieldsThisSourceMayAnswer(source.name())
		if want == 0 {
			continue
		}

		res := source.resolve(ctx, ref, want)
		recordSourceErr(&sourceErrs, seenErrs, res.sourceErr)
		mergeFactResult(&facts, &pending, want, res, notes)
	}

	applyFactDefaults(&facts, pending, ref)

	return facts, notes, sourceErrs
}

func recordSourceErr(sourceErrs *[]string, seen map[string]bool, sourceErr string) {
	if sourceErr == "" || seen[sourceErr] {
		return
	}
	seen[sourceErr] = true
	*sourceErrs = append(*sourceErrs, sourceErr)
}

func mergeFactResult(facts *ModelFacts, pending *fieldSet, want fieldSet, res sourceResult, notes map[factField][]string) {
	for _, m := range factMerges {
		bit := fieldSet(m.field)
		if want&bit == 0 {
			continue
		}
		if m.merge(facts, res.facts) {
			*pending &^= bit
			continue
		}
		if note, ok := res.notes[m.field]; ok && note != "" {
			notes[m.field] = append(notes[m.field], note)
		}
	}
}

func applyFactDefaults(facts *ModelFacts, pending fieldSet, ref modelRef) {
	contextWasPending := pending&fieldSet(fieldContextWindow) != 0
	if contextWasPending {
		facts.ContextWindow = Fact[int]{
			Value: defaultContextWindow, Known: true, Source: FactSourceFallback, Confidence: "low",
			Note: "conservative default",
		}
	}
	if pending&fieldSet(fieldMaxOutput) != 0 && contextWasPending {
		facts.MaxOutputTokens = Fact[int]{
			Value: defaultMaxOutputTokens, Known: true, Source: FactSourceFallback, Confidence: "low",
			Note: "conservative default",
		}
	}
	if pending&fieldSet(fieldEfforts) != 0 {
		facts.ReasoningEfforts = Fact[[]string]{Source: FactSourceUnknown, Confidence: "unknown"}
	}
	if pending&fieldSet(fieldTransport) != 0 {
		facts.Transport = Fact[transportChoice]{
			Value:      transportChoice{ProviderType: ref.Provider.Type, Transport: TransportConfigured, Reason: "none"},
			Known:      true,
			Source:     FactSourceFallback,
			Confidence: "high",
		}
	}
}
