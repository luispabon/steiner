package delegation

import (
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// TestBuildModelResolverWithResolveWiresProviderFactory proves the thin
// remaining logic at this layer: buildModelResolverWithResolve trims the
// alias, propagates resolve's result/error, and passes the resolved model
// through to ProviderFactory. Memoization and single-flight coalescing now
// live in provider.Resolver (see internal/provider/model_resolver_test.go);
// resolve here is a plain fake with no caching of its own.
func TestBuildModelResolverWithResolveWiresProviderFactory(t *testing.T) {
	t.Parallel()
	model := provider.ResolvedModel{Alias: "luna", BackendModelID: "luna-v1"}
	var gotAlias string
	resolve := func(alias string) (provider.ResolvedModel, error) {
		gotAlias = alias
		return model, nil
	}

	var factoryModel provider.ResolvedModel
	resolver := buildModelResolverWithResolve(DelegateDeps{
		ProviderFactory: func(got provider.ResolvedModel, _ string) (provider.Provider, error) {
			factoryModel = got
			return &stubProvider{}, nil
		},
	}, resolve)

	p, rm, err := resolver(" luna ")
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	if gotAlias != "luna" {
		t.Fatalf("resolve alias = %q, want trimmed %q", gotAlias, "luna")
	}
	if !reflect.DeepEqual(rm, model) {
		t.Fatalf("resolver() model = %#v, want %#v", rm, model)
	}
	if !reflect.DeepEqual(factoryModel, model) {
		t.Fatalf("ProviderFactory model = %#v, want %#v", factoryModel, model)
	}
	if p == nil {
		t.Fatal("resolver() provider = nil, want factory-built provider")
	}
}

func TestBuildModelResolverWithResolvePropagatesError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("resolution failure")
	resolver := buildModelResolverWithResolve(DelegateDeps{}, func(string) (provider.ResolvedModel, error) {
		return provider.ResolvedModel{}, wantErr
	})

	if _, _, err := resolver("luna"); !errors.Is(err, wantErr) {
		t.Fatalf("resolver() error = %v, want %v", err, wantErr)
	}
}

func TestBuildModelResolverWithResolveFallsBackToParentProviderWithoutFactory(t *testing.T) {
	t.Parallel()
	parentProvider := &stubProvider{}
	model := provider.ResolvedModel{Alias: "luna"}
	resolver := buildModelResolverWithResolve(DelegateDeps{Provider: parentProvider}, func(string) (provider.ResolvedModel, error) {
		return model, nil
	})

	p, rm, err := resolver("luna")
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	if p != provider.Provider(parentProvider) {
		t.Fatalf("resolver() provider = %#v, want parent provider", p)
	}
	if !reflect.DeepEqual(rm, model) {
		t.Fatalf("resolver() model = %#v, want %#v", rm, model)
	}
}
