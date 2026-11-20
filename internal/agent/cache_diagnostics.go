package agent

import (
	"context"
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
	UpstreamEndpoint     string `json:"upstream_endpoint,omitempty"`
	MessageCount         int    `json:"message_count,omitempty"`
	CallKind             string `json:"call_kind,omitempty"`
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
	// WireCache, when present, holds bounded fingerprints of the real wire
	// payload and affinity headers of the last delivery attempt. It is absent
	// for a provider without CacheFingerprinter, for a failed fingerprint, and
	// for every record written while the cache stream was disabled.
	WireCache *wireCachePayload `json:"wire,omitempty"`
	// ResumeBaselineUnavailable marks the first parent record after an
	// interactive resume whose comparison found no predecessor. Baselines are
	// process-local, so a resumed session normally has none; the flag records
	// that the baseline was unavailable, not that the resume caused a miss.
	// Absent means false.
	ResumeBaselineUnavailable bool `json:"resume_baseline_unavailable,omitempty"`

	// Measurement fields below describe how the call was served. A measured
	// zero or false value is omitted, so an absent field means zero or not
	// measured; fields without omitempty are emitted on every record.
	Attempts             int  `json:"attempts,omitempty"`
	CacheAffinityDropped bool `json:"cache_affinity_dropped,omitempty"`
	// ContextGeneration is the number of compaction stages applied within the
	// current run when the call was issued. It is emitted on every record; it
	// restarts at 0 for each run, and 0 means no compaction has applied to this
	// run yet.
	ContextGeneration int `json:"context_generation"`
	// PrefixPredecessorMessages is the message count of the predecessor request
	// that shared_prefix_messages was measured against. It is emitted only when
	// a predecessor is known, including a known empty one (0).
	PrefixPredecessorMessages *int `json:"prefix_predecessor_messages,omitempty"`
	// PrefixDivergenceRole is the role of the first request message that differs
	// from the predecessor. It is empty for an append of the predecessor, and
	// also when the current request is a strict prefix of the predecessor.
	PrefixDivergenceRole string `json:"prefix_divergence_role,omitempty"`
	// claude_subscription-only measurements.
	ClaudeSubProcess           string `json:"claudesub_process,omitempty"`
	ClaudeSubReseedReason      string `json:"claudesub_reseed_reason,omitempty"`
	ClaudeSubRecovery          string `json:"claudesub_recovery,omitempty"`
	ClaudeSubUsageGateMillis   int64  `json:"claudesub_usage_gate_ms,omitempty"`
	ClaudeSubUsageGatePolls    int    `json:"claudesub_usage_gate_polls,omitempty"`
	ClaudeSubUsageProbeSpawned bool   `json:"claudesub_usage_probe_spawned,omitempty"`
	// ClaudeSubSyncMismatchIndex is the request position where the live sync
	// record first failed to match, present only when that mismatch made the
	// call reseed. A pointer keeps a divergence at position zero distinct from
	// no mismatch.
	ClaudeSubSyncMismatchIndex *int   `json:"claudesub_sync_mismatch_index,omitempty"`
	ClaudeSubSyncMismatchRole  string `json:"claudesub_sync_mismatch_role,omitempty"`
	ClaudeSubSyncFailedCheck   string `json:"claudesub_sync_failed_check,omitempty"`
}

// wireCachePayload is the optional wire block of a cache record. Every value
// is an opaque digest, a flag, or a count derived from the payload and
// headers that were actually sent; it carries no prompt content, tool names or
// arguments, or key plaintext.
type wireCachePayload struct {
	CacheablePrefixHash string `json:"cacheable_prefix_hash,omitempty"`
	SharedPrefixHash    string `json:"shared_prefix_hash,omitempty"`
	BodyKeyHash         string `json:"body_key_hash,omitempty"`
	SessionIDHash       string `json:"session_id_hash,omitempty"`
	ThreadIDHash        string `json:"thread_id_hash,omitempty"`
	OriginatorHash      string `json:"originator_hash,omitempty"`
	AffinityKeysMatch   bool   `json:"affinity_keys_match"`
	// Stream reports how the last delivery attempt was sent: false for a
	// non-stream call, true for a streamed one.
	Stream       bool `json:"stream"`
	HasTools     bool `json:"has_tools"`
	ToolCount    int  `json:"tool_count,omitempty"`
	HasReasoning bool `json:"has_reasoning"`
	Store        bool `json:"store"`
}

// requestCacheStats carries the cache-prefix diagnostics computed for one
// outbound request that was actually issued, plus what promoteRequestCacheStats
// needs to later advance the baseline if the request is accepted.
type requestCacheStats struct {
	prefixHash           string
	sharedPrefixMessages int
	predecessorMessages  int
	divergenceRole       string
	comparisonEnabled    bool
	predecessorKnown     bool
	baselineKey          CacheBaselineKey
	messageHashes        []string
	// wireFingerprint and wireMeasured describe the last delivery attempt's
	// wire payload. wireMeasured is false when the provider has no wire
	// fingerprinter or the fingerprint call failed.
	wireFingerprint provider.WireCacheDiagnostics
	wireMeasured    bool
}

// emitCacheDiagnostic writes one kind: "cache" diagnostics record for a
// usage-bearing model response. No-op when the cache stream is disabled or
// the response carried no usage, mirroring recordModelUsage's own gate but
// deliberately independent of req.UsageRecorder: the diagnostics stream and
// the in-memory/persisted usagestats recorder are separate concerns gated by
// separate config.
func emitCacheDiagnostic(req RunRequest, response provider.ChatResponse, callKind string, turn int, stats requestCacheStats) {
	usage := response.Usage
	if usage == nil || !req.Diagnostics.Enabled(diagnostics.KindCache) {
		return
	}
	// A fallback target's call is not the run's cold start, so it reports
	// cold_start false and leaves the process-wide latch untouched.
	coldStart := !req.compactionFallbackTarget && !coldStartRecorded.Swap(true)
	var predecessorMessages *int
	if stats.predecessorKnown {
		count := stats.predecessorMessages
		predecessorMessages = &count
	}
	var wire *wireCachePayload
	if stats.wireMeasured {
		wire = &wireCachePayload{
			CacheablePrefixHash: stats.wireFingerprint.CacheablePrefixHash,
			SharedPrefixHash:    stats.wireFingerprint.SharedPrefixHash,
			BodyKeyHash:         stats.wireFingerprint.BodyKeyHash,
			SessionIDHash:       stats.wireFingerprint.SessionIDHash,
			ThreadIDHash:        stats.wireFingerprint.ThreadIDHash,
			OriginatorHash:      stats.wireFingerprint.OriginatorHash,
			AffinityKeysMatch:   stats.wireFingerprint.AffinityKeysMatch,
			Stream:              stats.wireFingerprint.Stream,
			HasTools:            stats.wireFingerprint.HasTools,
			ToolCount:           stats.wireFingerprint.ToolCount,
			HasReasoning:        stats.wireFingerprint.HasReasoning,
			Store:               stats.wireFingerprint.Store,
		}
	}
	payload := cachePayload{
		ProviderAlias:             req.ResolvedModel.ProviderAlias,
		BackendModelID:            req.ResolvedModel.BackendModelID,
		UpstreamEndpoint:          response.UpstreamEndpoint,
		MessageCount:              len(stats.messageHashes),
		CallKind:                  callKind,
		ProviderType:              string(req.ResolvedModel.EffectiveProviderType),
		PromptTokens:              usage.PromptTokens,
		CacheReadTokens:           usage.CacheReadInputTokens,
		CacheCreateTokens:         usage.CacheCreationInputTokens,
		CompletionTokens:          usage.CompletionTokens,
		ColdStart:                 coldStart,
		CacheKeyHash:              shortHash(req.PromptCacheKey),
		PrefixHash:                stats.prefixHash,
		SharedPrefixMessages:      stats.sharedPrefixMessages,
		PrefixPredecessorKnown:    stats.predecessorKnown,
		PrefixComparisonEnabled:   stats.comparisonEnabled,
		ContextGeneration:         req.contextGeneration,
		PrefixPredecessorMessages: predecessorMessages,
		PrefixDivergenceRole:      stats.divergenceRole,
		WireCache:                 wire,
		ResumeBaselineUnavailable: claimResumeBaselineUnavailable(req, stats),
	}
	if callDiagnostics := response.Diagnostics; callDiagnostics != nil {
		payload.Attempts = callDiagnostics.Attempts
		payload.CacheAffinityDropped = callDiagnostics.CacheAffinityDropped
		if claudeSub := callDiagnostics.ClaudeSub; claudeSub != nil {
			payload.ClaudeSubProcess = claudeSub.Process
			payload.ClaudeSubReseedReason = claudeSub.ReseedReason
			payload.ClaudeSubRecovery = claudeSub.Recovery
			payload.ClaudeSubUsageGateMillis = claudeSub.UsageGateMillis
			payload.ClaudeSubUsageGatePolls = claudeSub.UsageGatePolls
			payload.ClaudeSubUsageProbeSpawned = claudeSub.UsageProbeSpawned
			payload.ClaudeSubSyncMismatchIndex = claudeSub.SyncMismatchIndex
			payload.ClaudeSubSyncMismatchRole = claudeSub.SyncMismatchRole
			payload.ClaudeSubSyncFailedCheck = claudeSub.SyncMismatchCheck
		}
	}
	req.Diagnostics.Write(diagnostics.Record{
		Kind:      diagnostics.KindCache,
		Source:    req.UsageSource.DiagnosticsSource(),
		AgentID:   req.AgentID,
		AgentType: req.AgentType,
		Turn:      turn,
		Payload:   payload,
	})
}

// computeRequestCacheStats hashes the post-transform outbound messages and,
// when a baseline store is configured, compares them against the store's
// current baseline for this request's cache identity. It does not advance the
// baseline: call promoteRequestCacheStats once the request this was computed
// for is known to have been accepted. Call computeRequestCacheStats only for
// requests that are actually issued.
func computeRequestCacheStats(req RunRequest, messages []provider.Message) requestCacheStats {
	hashes := perMessageHashes(messages)
	stats := requestCacheStats{
		prefixHash:    cumulativePrefixHash(hashes),
		baselineKey:   cacheBaselineKeyForRequest(req),
		messageHashes: hashes,
	}
	// A comparison is only meaningful with a usable store and a nonempty cache
	// key; Compare is a no-op otherwise.
	if req.CacheBaseline != nil && req.PromptCacheKey != "" {
		stats.comparisonEnabled = true
		stats.sharedPrefixMessages, stats.predecessorMessages, stats.predecessorKnown = req.CacheBaseline.Compare(stats.baselineKey, hashes)
		stats.divergenceRole = prefixDivergenceRole(messages, stats.sharedPrefixMessages, stats.predecessorMessages, stats.predecessorKnown)
	}
	return stats
}

// prefixDivergenceRole returns the role of the first request message that
// differs from the predecessor. It is empty for an append of the predecessor
// and for a request that is a strict prefix of it, because neither rewrites a
// message the predecessor holds.
func prefixDivergenceRole(messages []provider.Message, sharedPrefixMessages, predecessorMessages int, predecessorKnown bool) string {
	if !predecessorKnown || sharedPrefixMessages >= predecessorMessages || sharedPrefixMessages >= len(messages) {
		return ""
	}
	return string(messages[sharedPrefixMessages].Role)
}

// promoteRequestCacheStats advances the cache baseline to the message
// sequence stats was computed from. Call it only once the request that
// produced stats is known to have been accepted by the provider: a rejected
// attempt must never become the predecessor for the next comparison. Safe to
// call unconditionally after computeRequestCacheStats; it is a no-op when
// comparison was not enabled for the request.
func promoteRequestCacheStats(req RunRequest, stats requestCacheStats) {
	if !stats.comparisonEnabled {
		return
	}
	req.CacheBaseline.Promote(stats.baselineKey, stats.messageHashes)
}

// wireCacheSendHook returns the per-delivery hook that fingerprints the
// request actually handed to the provider, including the streaming mode of
// that attempt. It returns nil unless the cache stream is enabled, so the
// extra payload serialization is paid only while diagnostics are on. A
// provider without a wire fingerprinter fails the capability check inside
// captureWireCacheFingerprint, before any payload is built.
func wireCacheSendHook(ctx context.Context, req RunRequest, stats *requestCacheStats) func(provider.ChatRequest, bool) {
	if !req.Diagnostics.Enabled(diagnostics.KindCache) {
		return nil
	}
	return func(sent provider.ChatRequest, stream bool) {
		stats.wireFingerprint, stats.wireMeasured = captureWireCacheFingerprint(ctx, req.Provider, sent, stream, *stats)
	}
}

// captureWireCacheFingerprint derives bounded wire diagnostics from the
// request a delivery attempt is about to send. cacheableMessages drops the
// trailing message, which is the newly appended prompt, tool result, or
// compaction instruction; sharedMessages is the measured common prefix with
// the predecessor when the comparison found one. A provider or payload that
// cannot be fingerprinted reports not measured instead of guessing.
func captureWireCacheFingerprint(ctx context.Context, prov provider.Provider, request provider.ChatRequest, stream bool, stats requestCacheStats) (provider.WireCacheDiagnostics, bool) {
	fingerprinter, ok := prov.(provider.CacheFingerprinter)
	if !ok {
		return provider.WireCacheDiagnostics{}, false
	}
	cacheableMessages := len(request.Messages) - 1
	if cacheableMessages < 0 {
		cacheableMessages = 0
	}
	sharedMessages := 0
	if stats.comparisonEnabled && stats.predecessorKnown {
		sharedMessages = stats.sharedPrefixMessages
	}
	fingerprint, err := fingerprinter.CacheFingerprint(ctx, request, stream, cacheableMessages, sharedMessages)
	if err != nil {
		return provider.WireCacheDiagnostics{}, false
	}
	return fingerprint, true
}

// cacheBaselineKeyForRequest builds the cache identity for a run's outbound
// requests from its prompt cache key, delegation AgentID, and resolved model
// identity. AgentID is included so that concurrent sub-agent siblings sharing
// one prompt cache key (see CacheKeyStore.KeyFor) still get isolated baseline
// entries: the parent run's AgentID is "", each child's is its own delegation
// AgentID.
func cacheBaselineKeyForRequest(req RunRequest) CacheBaselineKey {
	rm := req.ResolvedModel
	return CacheBaselineKey{
		CacheKey:               req.PromptCacheKey,
		AgentID:                req.AgentID,
		ConfiguredProviderType: string(rm.ProviderConfig.Type),
		EffectiveProviderType:  string(rm.EffectiveProviderType),
		EffectiveTransport:     string(rm.EffectiveTransport),
		ProviderAlias:          rm.ProviderAlias,
		BackendModelID:         rm.BackendModelID,
	}
}

// perMessageHashes returns one 8-hex-char content hash per message, computed
// over the provider.AppendMessageHashInput bytes. Order is preserved so the
// result doubles as the input to both cumulativePrefixHash and
// longestCommonPrefixLen. Fingerprints are appended into one buffer reused
// across messages and hashed in place, so this per-model-call pass never
// materializes a fingerprint string per message. An empty fingerprint keeps
// shortHash's empty-input result ("") rather than the digest of no bytes.
func perMessageHashes(messages []provider.Message) []string {
	hashes := make([]string, len(messages))
	var buf []byte
	for i, msg := range messages {
		buf = provider.AppendMessageHashInput(buf[:0], msg)
		if len(buf) == 0 {
			continue
		}
		sum := sha256.Sum256(buf)
		hashes[i] = hex.EncodeToString(sum[:4])
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
