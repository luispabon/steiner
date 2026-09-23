package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"sync/atomic"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/provider"
)

// coldStartRecorded latches to true after the first usage-bearing cache
// diagnostics record written by this process. It is intentionally
// process-scoped rather than per-run: a diagnostics Writer's RunID is fixed
// for the process's lifetime, so exactly one record should carry
// cold_start: true per run_id, whether that call belongs to the parent run
// or a delegated child.
var coldStartRecorded atomic.Bool

// cachePayload is the kind: "cache" diagnostics payload: one record per
// usage-bearing model response. Envelope fields (source, agent_type,
// agent_id, turn, build_sha) are supplied by diagnostics.Record itself.
type cachePayload struct {
	ProviderAlias        string `json:"provider_alias,omitempty"`
	BackendModelID       string `json:"backend_model_id,omitempty"`
	ProviderType         string `json:"provider_type,omitempty"`
	PromptTokens         int    `json:"prompt_tokens,omitempty"`
	CacheReadTokens      int    `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens    int    `json:"cache_create_tokens,omitempty"`
	CompletionTokens     int    `json:"completion_tokens,omitempty"`
	ColdStart            bool   `json:"cold_start,omitempty"`
	CacheKeyHash         string `json:"cache_key_hash,omitempty"`
	PrefixHash           string `json:"prefix_hash,omitempty"`
	SharedPrefixMessages int    `json:"shared_prefix_messages,omitempty"`
	// PrefixPredecessorKnown distinguishes "no predecessor to compare against"
	// from a known predecessor that shares zero messages. It is emitted on
	// every cache record (no omitempty).
	PrefixPredecessorKnown bool `json:"prefix_predecessor_known"`
	// PrefixComparisonEnabled reports whether a session baseline comparison was
	// active for this request: a usable store plus a nonempty cache key. When
	// false, PrefixPredecessorKnown is always false because nothing was
	// compared. Emitted on every cache record (no omitempty).
	PrefixComparisonEnabled bool `json:"prefix_comparison_enabled"`
}

// requestCacheStats carries the cache-prefix diagnostics computed for one
// outbound request that was actually issued.
type requestCacheStats struct {
	prefixHash           string
	sharedPrefixMessages int
	comparisonEnabled    bool
	predecessorKnown     bool
}

// emitCacheDiagnostic writes one kind: "cache" diagnostics record for a
// usage-bearing model response. No-op when the cache stream is disabled or
// the response carried no usage, mirroring recordModelUsage's own gate but
// deliberately independent of req.UsageRecorder: the diagnostics stream and
// the in-memory/persisted usagestats recorder are separate concerns gated by
// separate config.
func emitCacheDiagnostic(req RunRequest, usage *provider.UsageStats, turn int, stats requestCacheStats) {
	if usage == nil || !req.Diagnostics.Enabled(diagnostics.KindCache) {
		return
	}
	coldStart := !coldStartRecorded.Swap(true)
	req.Diagnostics.Write(diagnostics.Record{
		Kind:      diagnostics.KindCache,
		Source:    req.UsageSource.DiagnosticsSource(),
		AgentID:   req.AgentID,
		AgentType: req.AgentType,
		Turn:      turn,
		Payload: cachePayload{
			ProviderAlias:           req.ResolvedModel.ProviderAlias,
			BackendModelID:          req.ResolvedModel.BackendModelID,
			ProviderType:            string(req.ResolvedModel.EffectiveProviderType),
			PromptTokens:            usage.PromptTokens,
			CacheReadTokens:         usage.CacheReadInputTokens,
			CacheCreateTokens:       usage.CacheCreationInputTokens,
			CompletionTokens:        usage.CompletionTokens,
			ColdStart:               coldStart,
			CacheKeyHash:            shortHash(req.PromptCacheKey),
			PrefixHash:              stats.prefixHash,
			SharedPrefixMessages:    stats.sharedPrefixMessages,
			PrefixPredecessorKnown:  stats.predecessorKnown,
			PrefixComparisonEnabled: stats.comparisonEnabled,
		},
	})
}

// computeRequestCacheStats hashes the post-transform outbound messages and,
// when a baseline store is configured, compares them against and promotes them
// into the store for this request's cache identity. Call it only for requests
// that are actually issued.
func computeRequestCacheStats(req RunRequest, messages []provider.Message) requestCacheStats {
	hashes := perMessageHashes(messages)
	stats := requestCacheStats{prefixHash: cumulativePrefixHash(hashes)}
	// A comparison is only meaningful with a usable store and a nonempty cache
	// key; CompareAndPromote is a no-op otherwise.
	if req.CacheBaseline != nil && req.PromptCacheKey != "" {
		stats.comparisonEnabled = true
		stats.sharedPrefixMessages, stats.predecessorKnown = req.CacheBaseline.CompareAndPromote(cacheBaselineKeyForRequest(req), hashes)
	}
	return stats
}

// cacheBaselineKeyForRequest builds the cache identity for a run's outbound
// requests from its prompt cache key and resolved model identity.
func cacheBaselineKeyForRequest(req RunRequest) CacheBaselineKey {
	rm := req.ResolvedModel
	return CacheBaselineKey{
		CacheKey:               req.PromptCacheKey,
		ConfiguredProviderType: string(rm.ProviderConfig.Type),
		EffectiveProviderType:  string(rm.EffectiveProviderType),
		EffectiveTransport:     string(rm.EffectiveTransport),
		ProviderAlias:          rm.ProviderAlias,
		BackendModelID:         rm.BackendModelID,
	}
}

// perMessageHashes returns one 8-hex-char content hash per message, computed
// over provider.MessageHashInput. Order is preserved so the result doubles as
// the input to both cumulativePrefixHash and longestCommonPrefixLen.
func perMessageHashes(messages []provider.Message) []string {
	hashes := make([]string, len(messages))
	for i, msg := range messages {
		hashes[i] = shortHash(provider.MessageHashInput(msg))
	}
	return hashes
}

// cumulativePrefixHash folds hashes into a single rolling digest, one
// message at a time, so a pure append changes only the final value while a
// rewrite earlier in the sequence changes everything from that point on.
// shared_prefix_messages (longestCommonPrefixLen) is what actually tells the
// two cases apart; this value alone only signals "something changed".
func cumulativePrefixHash(hashes []string) string {
	cum := ""
	for _, h := range hashes {
		cum = shortHash(cum + h)
	}
	return cum
}

// longestCommonPrefixLen returns how many leading elements of prev and cur
// are identical, i.e. the index of the first divergence.
func longestCommonPrefixLen(prev, cur []string) int {
	n := len(prev)
	if len(cur) < n {
		n = len(cur)
	}
	i := 0
	for i < n && prev[i] == cur[i] {
		i++
	}
	return i
}

// shortHash returns an 8 hex char digest of s. Used for cache_key_hash,
// prefix_hash and per-message hashes so no diagnostics payload ever carries
// the prompt cache key or message content itself.
func shortHash(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}
