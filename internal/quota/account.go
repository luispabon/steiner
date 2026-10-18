package quota

import (
	"slices"
	"strings"
)

// Account describes one provider account whose quota is tracked. Key identifies
// the account, and Aliases are the provider names that resolve to it.
type Account struct {
	Key          string
	ProviderType string
	Aliases      []string
	Fetcher      Fetcher
	Unavailable  string
}

// APIKeyAccountKey returns the account key for the API-key provider alias. The
// key is "provider:" and the alias, so it does not change when the alias's API
// key, base URL, project, or provider type changes.
func APIKeyAccountKey(alias string) string {
	return "provider:" + alias
}

// legacyAPIKeyProviderTypes lists the provider types whose API-key accounts
// were keyed by a fingerprint before APIKeyAccountKey.
var legacyAPIKeyProviderTypes = []string{"opencode_go", "openrouter", "litellm"}

// IsLegacyAPIKeyAccountKey reports whether key is a fingerprint-format API-key
// account key: a provider type from legacyAPIKeyProviderTypes, a colon, and 16
// lowercase hex characters. Other keys, including non-API-key accounts, never
// match.
func IsLegacyAPIKeyAccountKey(key string) bool {
	providerType, fingerprint, ok := strings.Cut(key, ":")
	if !ok || len(fingerprint) != 16 || !slices.Contains(legacyAPIKeyProviderTypes, providerType) {
		return false
	}
	for _, c := range fingerprint {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
