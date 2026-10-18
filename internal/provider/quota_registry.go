package provider

import (
	"maps"
	"net/http"
	"slices"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/quota"
)

// QuotaRegistryDeps carries the runtime dependencies for BuildQuotaAccounts.
type QuotaRegistryDeps struct {
	// HTTPClient backs the Codex and API-key quota fetchers. Nil uses each
	// fetcher's default client.
	HTTPClient *http.Client
	// ClaudePool backs the claude_subscription fetcher. Nil marks that
	// account unavailable.
	ClaudePool *ClaudeSubscriptionPool
}

// BuildQuotaAccounts returns one quota.Account per provider alias whose type
// reports quota: codex, claude_subscription, opencode_go, openrouter, and
// litellm. Other provider types are skipped. Aliases are visited in sorted
// order, so the result is deterministic. An account that cannot be fetched
// carries an Unavailable reason and no Fetcher. BuildQuotaAccounts reads the
// stored Codex identity but makes no network calls and refreshes no tokens.
func BuildQuotaAccounts(providers map[string]config.ProviderConfig, deps QuotaRegistryDeps) []quota.Account {
	var accounts []quota.Account
	for _, alias := range slices.Sorted(maps.Keys(providers)) {
		cfg := providers[alias]
		var acct quota.Account
		switch cfg.Type {
		case config.ProviderTypeCodex:
			acct = codexQuotaAccount(deps.HTTPClient)
		case config.ProviderTypeClaudeSubscription:
			acct = claudeSubQuotaAccount(deps.ClaudePool)
		case config.ProviderTypeOpencodeGo, config.ProviderTypeOpenRouter, config.ProviderTypeLiteLLM:
			acct = apiKeyQuotaAccount(alias, cfg, deps.HTTPClient)
		default:
			continue
		}
		acct.Aliases = []string{alias}
		accounts = append(accounts, acct)
	}
	return accounts
}

// codexQuotaAccount describes the Codex account from the stored sign-in. A
// missing or unreadable sign-in is reported as not signed in; the registry has
// no error channel, so the load error is not surfaced.
func codexQuotaAccount(httpClient *http.Client) quota.Account {
	acct := quota.Account{ProviderType: string(config.ProviderTypeCodex)}
	identity, err := LoadCodexIdentity()
	if err != nil || !identity.SignedIn {
		acct.Key = CodexQuotaAccountKey("unknown")
		acct.Unavailable = "not signed in"
		return acct
	}
	acct.Key = CodexQuotaAccountKey(identity.AccountID)
	if identity.APIMode {
		acct.Unavailable = "API mode: no quota to report"
		return acct
	}
	acct.Fetcher = NewCodexQuotaFetcher(httpClient)
	return acct
}

// claudeSubQuotaAccount describes the claude_subscription account, which is
// fetched through the shared CLI pool.
func claudeSubQuotaAccount(pool *ClaudeSubscriptionPool) quota.Account {
	acct := quota.Account{
		Key:          ClaudeSubQuotaAccountKey,
		ProviderType: string(config.ProviderTypeClaudeSubscription),
	}
	if pool == nil {
		acct.Unavailable = "claude CLI pool unavailable"
		return acct
	}
	acct.Fetcher = NewClaudeSubQuotaFetcher(pool)
	return acct
}

// apiKeyQuotaAccount describes an opencode_go, openrouter, or litellm account
// for the provider alias. The config is resolved first so the profile default
// base URL and the api_key_env credential apply. The account key is the alias
// key, so it does not depend on the API key, base URL, or provider type.
func apiKeyQuotaAccount(alias string, cfg config.ProviderConfig, httpClient *http.Client) quota.Account {
	resolved := ResolveProviderConfig(cfg)
	providerType := string(resolved.Type)
	acct := quota.Account{
		Key:          quota.APIKeyAccountKey(alias),
		ProviderType: providerType,
	}
	if resolved.APIKey == "" {
		acct.Unavailable = "no API key"
		return acct
	}
	fetcher, err := NewAPIKeyQuotaFetcher(APIKeyQuotaConfig{
		ProviderType: providerType,
		BaseURL:      resolved.BaseURL,
		APIKey:       resolved.APIKey,
		AccountKey:   acct.Key,
	}, httpClient)
	if err != nil {
		// Unreachable for the three supported types; kept so a future type
		// mismatch reports unavailable instead of panicking.
		acct.Unavailable = "quota fetcher unavailable"
		return acct
	}
	acct.Fetcher = fetcher
	return acct
}
