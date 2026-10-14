package quota

import (
	"crypto/sha256"
	"encoding/hex"
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

// APIKeyAccountKey returns the account key for an API-key provider:
// providerType, a colon, and the first 16 hex characters of
// SHA-256(baseURL + "\x00" + apiKey). The raw API key never appears in the key.
func APIKeyAccountKey(providerType, baseURL, apiKey string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + apiKey))
	return providerType + ":" + hex.EncodeToString(sum[:])[:16]
}
