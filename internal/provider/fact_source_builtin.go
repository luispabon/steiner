package provider

import "context"

// builtinSource answers ReasoningEfforts from the curated built-in
// OpenAI/Codex reasoning family table.
type builtinSource struct{}

func (builtinSource) name() FactSource { return FactSourceFallback }

func (builtinSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	var facts ModelFacts
	if want&fieldSet(fieldEfforts) != 0 {
		if efforts, ok := openAIReasoningFallback(ref.Provider.Type, ref.BackendModelID); ok {
			facts.ReasoningEfforts = Fact[[]string]{
				Value: efforts, Known: true, Source: FactSourceFallback, Confidence: "low",
				Note: "built-in OpenAI reasoning families",
			}
		}
	}
	return sourceResult{facts: facts}
}
