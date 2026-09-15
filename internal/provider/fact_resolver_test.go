package provider

import (
	"context"
	"reflect"
	"testing"
)

// fakeFactSource is a configurable factSource for resolver unit tests.
type fakeFactSource struct {
	source      FactSource
	resolveFunc func(ctx context.Context, ref modelRef, want fieldSet) sourceResult
	calls       int
}

func (f *fakeFactSource) name() FactSource { return f.source }

func (f *fakeFactSource) resolve(ctx context.Context, ref modelRef, want fieldSet) sourceResult {
	f.calls++
	if f.resolveFunc != nil {
		return f.resolveFunc(ctx, ref, want)
	}
	return sourceResult{}
}

func TestResolveFactsPrecedence(t *testing.T) {
	configWins := &fakeFactSource{
		source: FactSourceConfig,
		resolveFunc: func(_ context.Context, _ modelRef, want fieldSet) sourceResult {
			var facts ModelFacts
			if want&fieldSet(fieldEfforts) != 0 {
				facts.ReasoningEfforts = Fact[[]string]{Value: []string{"low"}, Known: true, Source: FactSourceConfig}
			}
			return sourceResult{facts: facts}
		},
	}
	builtin := &fakeFactSource{
		source: FactSourceFallback,
		resolveFunc: func(_ context.Context, _ modelRef, want fieldSet) sourceResult {
			var facts ModelFacts
			if want&fieldSet(fieldEfforts) != 0 {
				facts.ReasoningEfforts = Fact[[]string]{Value: []string{"minimal", "low", "medium", "high"}, Known: true, Source: FactSourceFallback}
			}
			return sourceResult{facts: facts}
		},
	}
	noAnswer := &fakeFactSource{source: FactSourceConfig}

	tests := []struct {
		name       string
		sources    []factSource
		checkFacts func(t *testing.T, facts ModelFacts)
	}{
		{
			name:    "config wins over builtin for efforts",
			sources: []factSource{configWins, builtin},
			checkFacts: func(t *testing.T, facts ModelFacts) {
				if !reflect.DeepEqual(facts.ReasoningEfforts.Value, []string{"low"}) || facts.ReasoningEfforts.Source != FactSourceConfig {
					t.Errorf("ReasoningEfforts = %+v, want config value [low]", facts.ReasoningEfforts)
				}
			},
		},
		{
			name:    "builtin answers efforts when config doesn't",
			sources: []factSource{noAnswer, builtin},
			checkFacts: func(t *testing.T, facts ModelFacts) {
				if facts.ReasoningEfforts.Source != FactSourceFallback {
					t.Errorf("ReasoningEfforts.Source = %q, want fallback", facts.ReasoningEfforts.Source)
				}
			},
		},
		{
			name:    "context and output default when nothing answers",
			sources: nil,
			checkFacts: func(t *testing.T, facts ModelFacts) {
				if facts.ContextWindow.Value != 32768 || facts.ContextWindow.Source != FactSourceFallback {
					t.Errorf("ContextWindow = %+v, want default 32768/fallback", facts.ContextWindow)
				}
				if facts.MaxOutputTokens.Value != 4096 || facts.MaxOutputTokens.Source != FactSourceFallback {
					t.Errorf("MaxOutputTokens = %+v, want default 4096/fallback", facts.MaxOutputTokens)
				}
			},
		},
		{
			name: "output stays unknown when context is config-answered but output isn't",
			sources: []factSource{&fakeFactSource{
				source: FactSourceConfig,
				resolveFunc: func(_ context.Context, _ modelRef, want fieldSet) sourceResult {
					var facts ModelFacts
					if want&fieldSet(fieldContextWindow) != 0 {
						facts.ContextWindow = Fact[int]{Value: 128000, Known: true, Source: FactSourceConfig}
					}
					return sourceResult{facts: facts}
				},
			}},
			checkFacts: func(t *testing.T, facts ModelFacts) {
				if facts.ContextWindow.Value != 128000 || facts.ContextWindow.Source != FactSourceConfig {
					t.Errorf("ContextWindow = %+v, want config 128000", facts.ContextWindow)
				}
				if facts.MaxOutputTokens.Known {
					t.Errorf("MaxOutputTokens = %+v, want Known=false", facts.MaxOutputTokens)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts, _, _ := resolveFacts(context.Background(), modelRef{}, tt.sources)
			tt.checkFacts(t, facts)
		})
	}
}

func TestResolveFactsLaziness(t *testing.T) {
	highPrecedence := &fakeFactSource{
		source: FactSourceConfig,
		resolveFunc: func(_ context.Context, _ modelRef, _ fieldSet) sourceResult {
			return sourceResult{facts: ModelFacts{
				ContextWindow:     Fact[int]{Value: 1, Known: true, Source: FactSourceConfig},
				MaxOutputTokens:   Fact[int]{Value: 1, Known: true, Source: FactSourceConfig},
				Vision:            Fact[bool]{Value: true, Known: true, Source: FactSourceConfig},
				ReasoningEfforts:  Fact[[]string]{Value: []string{"low"}, Known: true, Source: FactSourceConfig},
				ReasoningEchoBack: Fact[bool]{Value: true, Known: true, Source: FactSourceConfig},
				Transport:         Fact[transportChoice]{Value: transportChoice{}, Known: true, Source: FactSourceConfig},
			}}
		},
	}
	lowPrecedence := &fakeFactSource{source: FactSourceFallback}

	_, _, _ = resolveFacts(context.Background(), modelRef{}, []factSource{highPrecedence, lowPrecedence})

	if lowPrecedence.calls != 0 {
		t.Errorf("lowPrecedence.calls = %d, want 0 (should be skipped once all its fields are resolved)", lowPrecedence.calls)
	}
}

func TestResolveFactsSourceErrDedup(t *testing.T) {
	source1 := &fakeFactSource{
		source: FactSourceConfig,
		resolveFunc: func(_ context.Context, _ modelRef, _ fieldSet) sourceResult {
			return sourceResult{sourceErr: "boom"}
		},
	}
	source2 := &fakeFactSource{
		source: FactSourceFallback,
		resolveFunc: func(_ context.Context, _ modelRef, _ fieldSet) sourceResult {
			return sourceResult{sourceErr: "boom"}
		},
	}

	_, _, errs := resolveFacts(context.Background(), modelRef{}, []factSource{source1, source2})
	if len(errs) != 1 || errs[0] != "boom" {
		t.Errorf("errs = %v, want [\"boom\"] deduped", errs)
	}
}
