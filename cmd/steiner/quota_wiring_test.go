package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/quota"
)

// quotaCountingFetcher counts fetches and always fails, so a refresh leaves no
// snapshot behind.
type quotaCountingFetcher struct{ calls int }

func (f *quotaCountingFetcher) Fetch(context.Context, quota.FetchOptions) (quota.Snapshot, error) {
	f.calls++
	return quota.Snapshot{}, errors.New("fake quota fetch")
}

// registerQuotaCountingAccount registers a fetchable account under alias and
// returns its fetcher.
func registerQuotaCountingAccount(svc *quota.Service, alias string) *quotaCountingFetcher {
	f := &quotaCountingFetcher{}
	svc.Register(quota.Account{Key: "test:" + alias, ProviderType: "test", Aliases: []string{alias}, Fetcher: f})
	return f
}

// isolateQuotaState points the quota store and home dir at temp dirs so an
// Observe call never writes real quota history.
func isolateQuotaState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

// assertQuotaObserved fails unless svc holds an observed snapshot for key.
func assertQuotaObserved(t *testing.T, svc *quota.Service, key string) {
	t.Helper()
	for _, v := range svc.Views("") {
		if v.Key == key && v.Latest != nil {
			return
		}
	}
	t.Errorf("no observed snapshot for %q in quota service views", key)
}

// TestRuntimeProviderFactoryMarksQuotaUsed pins that building a provider for a
// model route marks its alias used, so the next RefreshUsed fetches it.
func TestRuntimeProviderFactoryMarksQuotaUsed(t *testing.T) {
	svc := quota.NewService(quota.ServiceOptions{})
	fetcher := registerQuotaCountingAccount(svc, "claude")
	pool := provider.NewClaudeSubscriptionPool(provider.ClaudeSubscriptionPoolOptions{})
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("pool.Close() error = %v", err)
		}
	})
	factory := buildRuntimeProviderFactory(nil, nil, pool, svc)
	// The model alias differs from the provider alias. Quota accounts are keyed by
	// provider alias, so MarkUsed must receive ProviderAlias.
	rm := provider.ResolvedModel{
		Alias:          "claude-sonnet",
		ProviderAlias:  "claude",
		ProviderConfig: config.ProviderConfig{Type: config.ProviderTypeClaudeSubscription},
	}
	if rm.Alias == rm.ProviderAlias {
		t.Fatal("test model alias must differ from provider alias")
	}
	if _, err := factory(rm, "session"); err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	svc.RefreshUsed(context.Background(), 0)
	if fetcher.calls != 1 {
		t.Errorf("fetch calls = %d, want 1", fetcher.calls)
	}
}

// TestRuntimeProviderMarksQuotaUsedOnCachedCodexWS pins that the cached Codex
// WebSocket route marks its alias used. The fake factory does not mark quota,
// so only runtimeProvider can make RefreshUsed fetch.
func TestRuntimeProviderMarksQuotaUsedOnCachedCodexWS(t *testing.T) {
	svc := quota.NewService(quota.ServiceOptions{})
	fetcher := registerQuotaCountingAccount(svc, "codex")
	r := cliRunner{
		runtime: cliRuntime{
			providerFactory: func(provider.ResolvedModel, string) (provider.Provider, error) {
				return &fakeProvider{}, nil
			},
			codexWSProviderCache: provider.NewCodexWSCache(),
			quotaService:         svc,
		},
		promptCacheKeyFn: func() string { return "cache-key" },
	}
	rm := codexResolvedModel("codex-sonnet")
	rm.ProviderAlias = "codex"
	if rm.Alias == rm.ProviderAlias {
		t.Fatal("test model alias must differ from provider alias")
	}
	if _, err := r.runtimeProvider(rm); err != nil {
		t.Fatalf("runtimeProvider() error = %v", err)
	}
	svc.RefreshUsed(context.Background(), 0)
	if fetcher.calls != 1 {
		t.Errorf("fetch calls = %d, want 1", fetcher.calls)
	}
}

// TestRuntimeProviderNilQuotaServiceIsSafe pins that a runtime without a quota
// service still builds providers.
func TestRuntimeProviderNilQuotaServiceIsSafe(t *testing.T) {
	r := cliRunner{
		runtime: cliRuntime{
			providerFactory: func(provider.ResolvedModel, string) (provider.Provider, error) {
				return &fakeProvider{}, nil
			},
			codexWSProviderCache: provider.NewCodexWSCache(),
		},
		promptCacheKeyFn: func() string { return "cache-key" },
	}
	prov, err := r.runtimeProvider(codexResolvedModel("codex"))
	if err != nil || prov == nil {
		t.Errorf("runtimeProvider() = %v, %v; want a provider and nil error", prov, err)
	}
}

// TestCodexFactoryWiresQuotaObserver pins that the Codex client config built for
// both the HTTP and WebSocket transports observes into the runtime quota service.
func TestCodexFactoryWiresQuotaObserver(t *testing.T) {
	isolateQuotaState(t)
	setupCodexAuthFixture(t)
	tests := []struct {
		name      string
		websocket bool
	}{
		{name: "http"},
		{name: "websocket", websocket: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got provider.ClientConfig
			capture := func(cfg provider.ClientConfig) (provider.Provider, error) {
				got = cfg
				return &fakeProvider{}, nil
			}
			oldHTTP, oldWS := newCodexResponses, newCodexResponsesWS
			t.Cleanup(func() {
				newCodexResponses = oldHTTP
				newCodexResponsesWS = oldWS
			})
			newCodexResponses = capture
			newCodexResponsesWS = capture

			svc := quota.NewService(quota.ServiceOptions{})
			rm := codexResolvedModel("codex")
			if !tc.websocket {
				rm.ProviderConfig.Codex.Transport = ""
			}
			if _, err := buildRuntimeProviderFactory(nil, nil, nil, svc)(rm, "session"); err != nil {
				t.Fatalf("factory() error = %v", err)
			}
			if got.QuotaObserver == nil {
				t.Fatal("ClientConfig.QuotaObserver = nil, want the quota service observer")
			}
			got.QuotaObserver(quota.Snapshot{AccountKey: "codex:acct-123", ProviderType: "codex"})
			assertQuotaObserved(t, svc, "codex:acct-123")
		})
	}
}

// TestBuildRuntimeWiresClaudePoolQuotaObserver pins that the claude_subscription
// pool receives the runtime quota service's Observe as its QuotaObserver.
func TestBuildRuntimeWiresClaudePoolQuotaObserver(t *testing.T) {
	isolateQuotaState(t)
	var captured provider.ClaudeSubscriptionPoolOptions
	oldNew := newClaudeSubscriptionPool
	t.Cleanup(func() { newClaudeSubscriptionPool = oldNew })
	newClaudeSubscriptionPool = func(opts provider.ClaudeSubscriptionPoolOptions) *provider.ClaudeSubscriptionPool {
		captured = opts
		return oldNew(opts)
	}

	projectRoot := t.TempDir()
	configPath := filepath.Join(projectRoot, "config.yaml")
	writeFile(t, configPath, `providers:
  claude:
    type: claude_subscription
models:
  profiles:
    default:
      default_model: claude
  definitions:
    claude:
      provider: claude
      id: claude-sonnet
`)
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	flags := &cliFlags{configPath: configPath, trustProjectConfig: true, exec: true}

	rt, err := buildRuntimeWithRoots(context.Background(), cmd, flags, projectRoot, projectRoot, "")
	if err != nil {
		t.Fatalf("buildRuntimeWithRoots() error = %v", err)
	}
	t.Cleanup(func() { closeRuntime(&rt) })

	if captured.QuotaObserver == nil {
		t.Fatal("claude pool QuotaObserver = nil, want the quota service observer")
	}
	captured.QuotaObserver(quota.Snapshot{AccountKey: provider.ClaudeSubQuotaAccountKey, ProviderType: "claude_subscription"})
	assertQuotaObserved(t, rt.quotaService, provider.ClaudeSubQuotaAccountKey)
}

// TestPrimeQuotaRegistersConfiguredAccounts pins that interactive setup registers
// every quota-capable provider and skips the rest.
func TestPrimeQuotaRegistersConfiguredAccounts(t *testing.T) {
	isolateQuotaState(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := quota.NewService(quota.ServiceOptions{})
	rt := cliRuntime{
		cfg: config.Config{Providers: map[string]config.ProviderConfig{
			"claude": {Type: config.ProviderTypeClaudeSubscription},
			"local":  {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			"router": {Type: config.ProviderTypeOpenRouter, BaseURL: "https://openrouter.test/api/v1", APIKey: "sk-router"},
		}},
		quotaService: svc,
	}
	primeQuota(rt)

	views := svc.Views("")
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2 (claude, router)", len(views))
	}
	if views[0].DisplayAlias != "claude" || views[0].Unavailable != "claude CLI pool unavailable" {
		t.Errorf("claude view alias = %q, unavailable = %q", views[0].DisplayAlias, views[0].Unavailable)
	}
	if views[1].DisplayAlias != "router" || views[1].Unavailable != "" {
		t.Errorf("router view alias = %q, unavailable = %q; want router and empty", views[1].DisplayAlias, views[1].Unavailable)
	}
}

// TestPrimeQuotaLoadsStoreWithoutViews pins that primeQuota reads the store
// itself. Removing the file afterwards leaves the loaded snapshot visible.
func TestPrimeQuotaLoadsStoreWithoutViews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	store := quota.NewStore(path)
	now := time.Now()
	if err := store.Append(quota.Snapshot{AccountKey: provider.ClaudeSubQuotaAccountKey, FetchedAt: now}, now); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	svc := quota.NewService(quota.ServiceOptions{Store: store})
	primeQuota(cliRuntime{quotaService: svc})
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertQuotaObserved(t, svc, provider.ClaudeSubQuotaAccountKey)
}

// TestQuotaTUIConfigCarriesServiceAndInterval pins the quota service and the
// configured refresh interval handed to the TUI config.
func TestQuotaTUIConfigCarriesServiceAndInterval(t *testing.T) {
	svc := quota.NewService(quota.ServiceOptions{})
	tests := []struct {
		name     string
		interval string
		want     time.Duration
	}{
		{name: "configured interval", interval: "2m", want: 2 * time.Minute},
		{name: "zero disables refresh", interval: "0s", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := cliRuntime{
				cfg:          config.Config{TUI: config.TUIConfig{QuotaRefreshInterval: config.MustDuration(tc.interval)}},
				quotaService: svc,
			}
			gotSvc, gotInterval := quotaTUIConfig(rt)
			if gotSvc != svc {
				t.Errorf("quota service = %p, want %p", gotSvc, svc)
			}
			if gotInterval != tc.want {
				t.Errorf("refresh interval = %v, want %v", gotInterval, tc.want)
			}
		})
	}
}

// TestBuildRuntimeQuotaServiceFallsBackToMemory pins that an unresolvable store
// path yields a working in-memory service instead of failing the build.
func TestBuildRuntimeQuotaServiceFallsBackToMemory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	svc := buildRuntimeQuotaService()
	if svc == nil {
		t.Fatal("buildRuntimeQuotaService() = nil")
	}
	registerQuotaCountingAccount(svc, "x")
	svc.Observe(quota.Snapshot{AccountKey: "test:x", ProviderType: "test"})
	views := svc.Views("")
	if len(views) != 1 || views[0].Latest == nil {
		t.Fatalf("views = %+v, want one view with a latest snapshot", views)
	}
}
