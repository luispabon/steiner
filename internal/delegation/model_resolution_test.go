package delegation

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestMemoizedModelResolverConcurrentAliasSharesModelAndCreatesProvidersPerCaller(t *testing.T) {
	var resolveCalls atomic.Int32
	resolveStarted := make(chan struct{})
	releaseResolve := make(chan struct{})
	model := provider.ResolvedModel{Alias: "luna", BackendModelID: "luna-v1"}
	resolve := func(alias string) (provider.ResolvedModel, error) {
		if resolveCalls.Add(1) == 1 {
			close(resolveStarted)
			<-releaseResolve
		}
		if alias != "luna" {
			t.Errorf("resolve alias = %q, want luna", alias)
		}
		return model, nil
	}

	var factoryCalls atomic.Int32
	resolver := buildModelResolverWithResolve(DelegateDeps{
		ProviderFactory: func(got provider.ResolvedModel, _ string) (provider.Provider, error) {
			if !reflect.DeepEqual(got, model) {
				t.Errorf("factory model = %#v, want %#v", got, model)
			}
			factoryCalls.Add(1)
			return &stubProvider{}, nil
		},
	}, resolve)

	const callers = 16
	results := make([]provider.ResolvedModel, callers)
	providers := make([]provider.Provider, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		providers[0], results[0], errs[0] = resolver(" luna ")
	}()
	<-resolveStarted

	wg.Add(callers - 1)
	for i := 1; i < callers; i++ {
		i := i
		go func() {
			defer wg.Done()
			providers[i], results[i], errs[i] = resolver("luna")
		}()
	}
	close(releaseResolve)
	wg.Wait()

	if got := resolveCalls.Load(); got != 1 {
		t.Fatalf("underlying resolver calls = %d, want 1", got)
	}
	if got := factoryCalls.Load(); got != callers {
		t.Fatalf("ProviderFactory calls = %d, want %d", got, callers)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("resolver call %d error = %v", i, errs[i])
		}
		if !reflect.DeepEqual(results[i], model) {
			t.Errorf("resolver call %d model = %#v, want %#v", i, results[i], model)
		}
		for j := 0; j < i; j++ {
			if providers[i] == providers[j] {
				t.Fatalf("resolver calls %d and %d returned the same provider instance", i, j)
			}
		}
	}
}

func TestMemoizedModelResolverDoesNotCacheFailures(t *testing.T) {
	var resolveCalls atomic.Int32
	wantErr := errors.New("temporary resolution failure")
	model := provider.ResolvedModel{Alias: "luna", BackendModelID: "luna-v1"}
	resolver := newMemoizedModelResolver(func(string) (provider.ResolvedModel, error) {
		if resolveCalls.Add(1) == 1 {
			return provider.ResolvedModel{}, wantErr
		}
		return model, nil
	})

	if _, err := resolver("luna"); !errors.Is(err, wantErr) {
		t.Fatalf("first resolution error = %v, want %v", err, wantErr)
	}
	got, err := resolver("luna")
	if err != nil {
		t.Fatalf("second resolution error = %v", err)
	}
	if !reflect.DeepEqual(got, model) {
		t.Fatalf("second resolution model = %#v, want %#v", got, model)
	}
	if got := resolveCalls.Load(); got != 2 {
		t.Fatalf("underlying resolver calls = %d, want 2", got)
	}
}

func TestMemoizedModelResolverKeepsAliasesIndependent(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	resolver := newMemoizedModelResolver(func(alias string) (provider.ResolvedModel, error) {
		mu.Lock()
		calls[alias]++
		mu.Unlock()
		return provider.ResolvedModel{Alias: alias}, nil
	})

	first, err := resolver("luna")
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver("nova")
	if err != nil {
		t.Fatal(err)
	}
	if first.Alias != "luna" || second.Alias != "nova" {
		t.Fatalf("resolved aliases = %q, %q", first.Alias, second.Alias)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["luna"] != 1 || calls["nova"] != 1 {
		t.Fatalf("resolution calls = %#v, want one per alias", calls)
	}
}
