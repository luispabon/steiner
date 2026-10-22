package quota

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
