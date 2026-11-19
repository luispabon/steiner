package interactive

import (
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// storedLastMainRequest converts a live last main request to its persisted
// form, returning nil for the zero value so the JSON field stays absent.
func storedLastMainRequest(last agent.LastMainRequest) *session.LastMainRequest {
	if last == (agent.LastMainRequest{}) {
		return nil
	}
	return &session.LastMainRequest{
		At:             last.At,
		ProviderAlias:  last.ProviderAlias,
		BackendModelID: last.BackendModelID,
		PromptCacheKey: last.PromptCacheKey,
	}
}

// restoreLastMainRequest converts a persisted last main request back to its
// live form. It returns the zero value when the record is absent, has no start
// time or one in the future, lacks a model identity, or names another prompt
// cache key. The run that consumes the seed checks the identity against its own
// request again (see agent.seedLastMainRequest).
func restoreLastMainRequest(stored *session.LastMainRequest, cacheKey string, now time.Time) agent.LastMainRequest {
	if stored == nil {
		return agent.LastMainRequest{}
	}
	last := agent.LastMainRequest{
		At:             stored.At,
		ProviderAlias:  stored.ProviderAlias,
		BackendModelID: stored.BackendModelID,
		PromptCacheKey: stored.PromptCacheKey,
	}
	if last.At.IsZero() || last.At.After(now) ||
		last.ProviderAlias == "" || last.BackendModelID == "" ||
		last.PromptCacheKey == "" || last.PromptCacheKey != cacheKey {
		return agent.LastMainRequest{}
	}
	return last
}
