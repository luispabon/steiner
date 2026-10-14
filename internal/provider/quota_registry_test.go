package provider

import (
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/quota"
)

const quotaRegistryTestKeyEnv = "STEINER_QUOTA_REGISTRY_TEST_KEY"

// quotaRegistryTestIsolate points the Codex token path and the home directory
// at fresh temp dirs so tests never read or write real credentials.
func quotaRegistryTestIsolate(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return codexQuotaTestTokenPath(t)
}

func TestBuildQuotaAccountsCodex(t *testing.T) {
	tests := []struct {
		name            string
		token           string
		wantKey         string
		wantUnavailable string
		wantFetcher     bool
	}{
		{
			name:            "no stored sign-in",
			wantKey:         "codex:unknown",
			wantUnavailable: "not signed in",
		},
		{
			name:            "unreadable token",
			token:           "{not json",
			wantKey:         "codex:unknown",
			wantUnavailable: "not signed in",
		},
		{
			name:            "api mode",
			token:           `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1","openai_api_key":"sk-1"}`,
			wantKey:         "codex:acct-1",
			wantUnavailable: "API mode: no quota to report",
		},
		{
			name:        "signed in",
			token:       `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`,
			wantKey:     "codex:acct-1",
			wantFetcher: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := quotaRegistryTestIsolate(t)
			if tc.token != "" {
				codexQuotaTestWriteToken(t, path, tc.token)
			}
			got := BuildQuotaAccounts(map[string]config.ProviderConfig{
				"codex": {Type: config.ProviderTypeCodex},
			}, QuotaRegistryDeps{})
			if len(got) != 1 {
				t.Fatalf("accounts = %d, want 1", len(got))
			}
			assertQuotaAccount(t, got[0], quotaAccountWant{
				key:          tc.wantKey,
				providerType: "codex",
				alias:        "codex",
				unavailable:  tc.wantUnavailable,
				wantFetcher:  tc.wantFetcher,
			})
		})
	}
}

func TestBuildQuotaAccountsClaudeSubscription(t *testing.T) {
	quotaRegistryTestIsolate(t)
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("pool.Close() error = %v", err)
		}
	})

	tests := []struct {
		name            string
		pool            *ClaudeSubscriptionPool
		wantUnavailable string
		wantFetcher     bool
	}{
		{name: "nil pool", wantUnavailable: "claude CLI pool unavailable"},
		{name: "pool present", pool: pool, wantFetcher: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildQuotaAccounts(map[string]config.ProviderConfig{
				"claude": {Type: config.ProviderTypeClaudeSubscription},
			}, QuotaRegistryDeps{ClaudePool: tc.pool})
			if len(got) != 1 {
				t.Fatalf("accounts = %d, want 1", len(got))
			}
			assertQuotaAccount(t, got[0], quotaAccountWant{
				key:          ClaudeSubQuotaAccountKey,
				providerType: "claude_subscription",
				alias:        "claude",
				unavailable:  tc.wantUnavailable,
				wantFetcher:  tc.wantFetcher,
			})
		})
	}
}

func TestBuildQuotaAccountsAPIKey(t *testing.T) {
	quotaRegistryTestIsolate(t)

	const (
		litellmBase = "https://litellm.test"
		goBase      = "https://opencode.ai/zen/go/v1"
	)
	tests := []struct {
		name            string
		cfg             config.ProviderConfig
		env             string
		wantType        string
		wantKey         string
		wantUnavailable string
	}{
		{
			name:     "opencode_go literal key",
			cfg:      config.ProviderConfig{Type: config.ProviderTypeOpencodeGo, BaseURL: "https://opencode.test", APIKey: "sk-go"},
			wantType: "opencode_go",
			wantKey:  quota.APIKeyAccountKey("opencode_go", "https://opencode.test", "sk-go"),
		},
		{
			name:     "opencode_go default base URL",
			cfg:      config.ProviderConfig{Type: config.ProviderTypeOpencodeGo, APIKey: "sk-go"},
			wantType: "opencode_go",
			wantKey:  quota.APIKeyAccountKey("opencode_go", goBase, "sk-go"),
		},
		{
			name:     "openrouter key from api_key_env",
			cfg:      config.ProviderConfig{Type: config.ProviderTypeOpenRouter, BaseURL: "https://openrouter.test/api/v1", APIKeyEnv: quotaRegistryTestKeyEnv},
			env:      "sk-env",
			wantType: "openrouter",
			wantKey:  quota.APIKeyAccountKey("openrouter", "https://openrouter.test/api/v1", "sk-env"),
		},
		{
			name:            "openrouter empty api_key_env",
			cfg:             config.ProviderConfig{Type: config.ProviderTypeOpenRouter, BaseURL: "https://openrouter.test/api/v1", APIKeyEnv: quotaRegistryTestKeyEnv},
			env:             "",
			wantType:        "openrouter",
			wantKey:         quota.APIKeyAccountKey("openrouter", "https://openrouter.test/api/v1", ""),
			wantUnavailable: "no API key",
		},
		{
			name:            "litellm without key",
			cfg:             config.ProviderConfig{Type: config.ProviderTypeLiteLLM, BaseURL: litellmBase},
			wantType:        "litellm",
			wantKey:         quota.APIKeyAccountKey("litellm", litellmBase, ""),
			wantUnavailable: "no API key",
		},
		{
			name:     "litellm literal key",
			cfg:      config.ProviderConfig{Type: config.ProviderTypeLiteLLM, BaseURL: litellmBase, APIKey: "sk-litellm"},
			wantType: "litellm",
			wantKey:  quota.APIKeyAccountKey("litellm", litellmBase, "sk-litellm"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(quotaRegistryTestKeyEnv, tc.env)
			got := BuildQuotaAccounts(map[string]config.ProviderConfig{"api": tc.cfg}, QuotaRegistryDeps{})
			if len(got) != 1 {
				t.Fatalf("accounts = %d, want 1", len(got))
			}
			assertQuotaAccount(t, got[0], quotaAccountWant{
				key:          tc.wantKey,
				providerType: tc.wantType,
				alias:        "api",
				unavailable:  tc.wantUnavailable,
				wantFetcher:  tc.wantUnavailable == "",
			})
		})
	}
}

func TestBuildQuotaAccountsSkipsUnsupportedTypes(t *testing.T) {
	quotaRegistryTestIsolate(t)

	for _, typ := range []config.ProviderType{
		config.ProviderTypeOpenAICompat,
		config.ProviderTypeOllama,
		config.ProviderTypeLMStudio,
		config.ProviderTypeOpenAI,
		config.ProviderTypeAnthropic,
		config.ProviderTypeGemini,
		config.ProviderTypeOpencodeZen,
	} {
		t.Run(string(typ), func(t *testing.T) {
			got := BuildQuotaAccounts(map[string]config.ProviderConfig{
				"skipped": {Type: typ, BaseURL: "https://example.test", APIKey: "sk"},
			}, QuotaRegistryDeps{})
			if len(got) != 0 {
				t.Errorf("accounts = %d, want 0 for type %s", len(got), typ)
			}
		})
	}
}

func TestBuildQuotaAccountsOrdersAliasesDeterministically(t *testing.T) {
	quotaRegistryTestIsolate(t)

	providers := map[string]config.ProviderConfig{
		"zeta":  {Type: config.ProviderTypeOpenRouter, APIKey: "sk-z"},
		"alpha": {Type: config.ProviderTypeOpenRouter, APIKey: "sk-a"},
		"mid":   {Type: config.ProviderTypeLiteLLM, BaseURL: "https://litellm.test", APIKey: "sk-m"},
	}
	want := []string{"alpha", "mid", "zeta"}
	for i := range 20 {
		got := BuildQuotaAccounts(providers, QuotaRegistryDeps{})
		var aliases []string
		for _, acct := range got {
			aliases = append(aliases, acct.Aliases...)
		}
		if !slices.Equal(aliases, want) {
			t.Fatalf("run %d: aliases = %q, want %q", i, aliases, want)
		}
	}
}

func TestQuotaRegistryMergesCodexAliases(t *testing.T) {
	path := quotaRegistryTestIsolate(t)
	codexQuotaTestWriteToken(t, path, `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`)

	svc := quota.NewService(quota.ServiceOptions{})
	for _, acct := range BuildQuotaAccounts(map[string]config.ProviderConfig{
		"codex-b": {Type: config.ProviderTypeCodex},
		"codex-a": {Type: config.ProviderTypeCodex},
	}, QuotaRegistryDeps{}) {
		svc.Register(acct)
	}

	views := svc.Views("")
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1 merged Codex account", len(views))
	}
	if views[0].Key != "codex:acct-1" || views[0].DisplayAlias != "codex-a" {
		t.Errorf("view = key %q alias %q, want codex:acct-1 and codex-a", views[0].Key, views[0].DisplayAlias)
	}
	for _, alias := range []string{"codex-a", "codex-b"} {
		if key, ok := svc.AccountKeyForAlias(alias); !ok || key != "codex:acct-1" {
			t.Errorf("AccountKeyForAlias(%q) = %q, %v; want codex:acct-1, true", alias, key, ok)
		}
	}
}

// quotaAccountWant describes the expected fields of one quota.Account.
type quotaAccountWant struct {
	key          string
	providerType string
	alias        string
	unavailable  string
	wantFetcher  bool
}

func assertQuotaAccount(t *testing.T, got quota.Account, want quotaAccountWant) {
	t.Helper()
	if got.Key != want.key {
		t.Errorf("Key = %q, want %q", got.Key, want.key)
	}
	if got.ProviderType != want.providerType {
		t.Errorf("ProviderType = %q, want %q", got.ProviderType, want.providerType)
	}
	if !slices.Equal(got.Aliases, []string{want.alias}) {
		t.Errorf("Aliases = %q, want [%q]", got.Aliases, want.alias)
	}
	if got.Unavailable != want.unavailable {
		t.Errorf("Unavailable = %q, want %q", got.Unavailable, want.unavailable)
	}
	if (got.Fetcher != nil) != want.wantFetcher {
		t.Errorf("Fetcher set = %t, want %t", got.Fetcher != nil, want.wantFetcher)
	}
}
