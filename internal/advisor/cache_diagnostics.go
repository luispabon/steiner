package advisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/provider"
)

type cachePayload struct {
	ProviderAlias        string `json:"provider_alias,omitempty"`
	BackendModelID       string `json:"backend_model_id,omitempty"`
	ProviderType         string `json:"provider_type,omitempty"`
	PromptTokens         int    `json:"prompt_tokens,omitempty"`
	CacheReadTokens      int    `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens    int    `json:"cache_create_tokens,omitempty"`
	CompletionTokens     int    `json:"completion_tokens,omitempty"`
	CacheKeyHash         string `json:"cache_key_hash,omitempty"`
	PrefixHash           string `json:"prefix_hash,omitempty"`
	PrefixMessageCount   int    `json:"prefix_message_count,omitempty"`
	SharedPrefixMessages int    `json:"shared_prefix_messages,omitempty"`
	CacheablePrefixHash  string `json:"cacheable_prefix_hash,omitempty"`
	SharedPrefixHash     string `json:"shared_prefix_hash,omitempty"`
	BodyKeyHash          string `json:"body_key_hash,omitempty"`
	SessionIDHash        string `json:"session_id_hash,omitempty"`
	ThreadIDHash         string `json:"thread_id_hash,omitempty"`
	OriginatorHash       string `json:"originator_hash,omitempty"`
	AffinityKeysMatch    bool   `json:"affinity_keys_match"`
	Stream               bool   `json:"stream"`
	HasTools             bool   `json:"has_tools"`
	ToolCount            int    `json:"tool_count"`
	HasReasoning         bool   `json:"has_reasoning"`
	Store                bool   `json:"store"`
}

func (s *handlerState) emitCacheDiagnostic(writer *diagnostics.Writer, model provider.ResolvedModel, usage *provider.UsageStats, messages []provider.Message, fingerprints ...provider.WireCacheDiagnostics) {
	var fingerprint provider.WireCacheDiagnostics
	if len(fingerprints) > 0 {
		fingerprint = fingerprints[0]
	}
	if writer == nil || !writer.Enabled(diagnostics.KindCache) {
		return
	}
	prefix := messages
	if len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	current := perMessageHashes(prefix)
	s.cacheMu.Lock()
	shared := longestCommonPrefixLen(s.previousPrefix, current)
	s.previousPrefix = append([]string(nil), current...)
	s.cacheMu.Unlock()
	var prompt, read, create, completion int
	if usage != nil {
		prompt, read, create, completion = usage.PromptTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.CompletionTokens
	}
	writer.Write(diagnostics.Record{Kind: diagnostics.KindCache, Source: diagnostics.SourceAdvisor, Payload: cachePayload{
		ProviderAlias: model.ProviderAlias, BackendModelID: model.BackendModelID, ProviderType: string(model.EffectiveProviderType),
		PromptTokens: prompt, CacheReadTokens: read, CacheCreateTokens: create, CompletionTokens: completion,
		CacheKeyHash: shortHash(s.cacheKey), PrefixHash: hashMessages(prefix), PrefixMessageCount: len(prefix), SharedPrefixMessages: shared,
		CacheablePrefixHash: fingerprint.CacheablePrefixHash, SharedPrefixHash: fingerprint.SharedPrefixHash,
		BodyKeyHash: fingerprint.BodyKeyHash, SessionIDHash: fingerprint.SessionIDHash, ThreadIDHash: fingerprint.ThreadIDHash,
		OriginatorHash: fingerprint.OriginatorHash, AffinityKeysMatch: fingerprint.AffinityKeysMatch, Stream: fingerprint.Stream,
		HasTools: fingerprint.HasTools, ToolCount: fingerprint.ToolCount, HasReasoning: fingerprint.HasReasoning, Store: fingerprint.Store,
	}})
}

func emitWireCacheDiagnostic(ctx context.Context, prov provider.Provider, request provider.ChatRequest, stream bool, diagnostic *advisorDiagnosticContext) {
	if diagnostic == nil || diagnostic.writer == nil || !diagnostic.writer.Enabled(diagnostics.KindCache) || diagnostic.state == nil {
		return
	}
	fingerprinter, ok := prov.(provider.CacheFingerprinter)
	if !ok {
		return
	}
	prefix := request.Messages
	if len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	current := perMessageHashes(prefix)
	diagnostic.state.cacheMu.Lock()
	shared := longestCommonPrefixLen(diagnostic.state.previousPrefix, current)
	diagnostic.state.cacheMu.Unlock()
	fingerprint, err := fingerprinter.CacheFingerprint(ctx, request, stream, len(prefix), shared)
	if err == nil {
		diagnostic.fingerprint = fingerprint
	}
}

func perMessageHashes(messages []provider.Message) []string {
	hashes := make([]string, len(messages))
	for i, message := range messages {
		hashes[i] = shortHash(canonicalMessage(message))
	}
	return hashes
}

func canonicalMessage(message provider.Message) string {
	data, err := json.Marshal(message)
	if err == nil {
		return string(data)
	}
	return fmt.Sprintf("json-marshal-error:%v;message:%T:%#v", err, message, message)
}

func hashMessages(messages []provider.Message) string {
	var data string
	for _, message := range messages {
		canonical := canonicalMessage(message)
		data += strconv.Itoa(len(canonical)) + ":" + canonical
	}
	if data == "" {
		return ""
	}
	return shortHash(data)
}
func longestCommonPrefixLen(previous, current []string) int {
	n := len(previous)
	if len(current) < n {
		n = len(current)
	}
	for i := 0; i < n; i++ {
		if previous[i] != current[i] {
			return i
		}
	}
	return n
}
func shortHash(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:4])
}
