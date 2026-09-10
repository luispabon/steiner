package advisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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

//nolint:unparam // model remains part of the diagnostic emission seam.
func (s *handlerState) emitCacheDiagnostic(writer *diagnostics.Writer, model provider.ResolvedModel, usage *provider.UsageStats, messages []provider.Message, fingerprints ...provider.WireCacheDiagnostics) {
	diagnostic := &advisorDiagnosticContext{state: s, writer: writer}
	s.emitCacheDiagnosticWithContext(writer, model, usage, messages, firstFingerprint(fingerprints), diagnostic)
}

func (s *handlerState) emitCacheDiagnosticWithContext(writer *diagnostics.Writer, model provider.ResolvedModel, usage *provider.UsageStats, messages []provider.Message, fingerprint provider.WireCacheDiagnostics, diagnostic *advisorDiagnosticContext) {
	if writer == nil || !writer.Enabled(diagnostics.KindCache) || diagnostic == nil {
		return
	}
	if s == nil {
		return
	}
	if s.shared == nil {
		s.shared = NewSharedState()
	}
	prepareCacheDiagnostic(diagnostic, messages)
	if !diagnostic.prepared {
		return
	}
	var prompt, read, create, completion int
	if usage != nil {
		prompt, read, create, completion = usage.PromptTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.CompletionTokens
	}
	writer.Write(diagnostics.Record{Kind: diagnostics.KindCache, Source: diagnostics.SourceAdvisor, Payload: cachePayload{
		ProviderAlias: model.ProviderAlias, BackendModelID: model.BackendModelID, ProviderType: string(model.EffectiveProviderType),
		PromptTokens: prompt, CacheReadTokens: read, CacheCreateTokens: create, CompletionTokens: completion,
		CacheKeyHash: shortHash(s.cacheKey), PrefixHash: hashMessages(prefixMessages(messages)), PrefixMessageCount: len(diagnostic.prefixHashes), SharedPrefixMessages: diagnostic.sharedPrefixMessages,
		CacheablePrefixHash: fingerprint.CacheablePrefixHash, SharedPrefixHash: fingerprint.SharedPrefixHash,
		BodyKeyHash: fingerprint.BodyKeyHash, SessionIDHash: fingerprint.SessionIDHash, ThreadIDHash: fingerprint.ThreadIDHash,
		OriginatorHash: fingerprint.OriginatorHash, AffinityKeysMatch: fingerprint.AffinityKeysMatch, Stream: fingerprint.Stream,
		HasTools: fingerprint.HasTools, ToolCount: fingerprint.ToolCount, HasReasoning: fingerprint.HasReasoning, Store: fingerprint.Store,
	}})
	commitCacheDiagnostic(diagnostic)
}

func firstFingerprint(fingerprints []provider.WireCacheDiagnostics) provider.WireCacheDiagnostics {
	if len(fingerprints) == 0 {
		return provider.WireCacheDiagnostics{}
	}
	return fingerprints[0]
}

func prepareCacheDiagnostic(diagnostic *advisorDiagnosticContext, messages []provider.Message) {
	if diagnostic == nil || diagnostic.prepared || diagnostic.writer == nil || !diagnostic.writer.Enabled(diagnostics.KindCache) || diagnostic.state == nil || diagnostic.state.shared == nil {
		return
	}
	prefix := prefixMessages(messages)
	current := perMessageHashes(prefix)
	diagnostic.state.shared.cacheMu.Lock()
	diagnostic.sharedPrefixMessages = longestCommonPrefixLen(diagnostic.state.shared.previousPrefix, current)
	diagnostic.state.shared.cacheMu.Unlock()
	diagnostic.prefixHashes = current
	diagnostic.prepared = true
}

func commitCacheDiagnostic(diagnostic *advisorDiagnosticContext) {
	if diagnostic == nil || !diagnostic.prepared || diagnostic.state == nil || diagnostic.state.shared == nil {
		return
	}
	diagnostic.state.shared.cacheMu.Lock()
	diagnostic.state.shared.previousPrefix = append([]string(nil), diagnostic.prefixHashes...)
	diagnostic.state.shared.cacheMu.Unlock()
}

func prefixMessages(messages []provider.Message) []provider.Message {
	prefix := messages
	if len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func emitWireCacheDiagnostic(ctx context.Context, prov provider.Provider, request provider.ChatRequest, stream bool, diagnostic *advisorDiagnosticContext) {
	if diagnostic == nil || diagnostic.writer == nil || !diagnostic.writer.Enabled(diagnostics.KindCache) || diagnostic.state == nil {
		return
	}
	if diagnostic.state.shared == nil {
		diagnostic.state.shared = NewSharedState()
	}
	prepareCacheDiagnostic(diagnostic, request.Messages)
	if !diagnostic.prepared {
		return
	}
	fingerprinter, ok := prov.(provider.CacheFingerprinter)
	if !ok {
		return
	}
	fingerprint, err := fingerprinter.CacheFingerprint(ctx, request, stream, len(diagnostic.prefixHashes), diagnostic.sharedPrefixMessages)
	if err == nil {
		diagnostic.fingerprint = fingerprint
	}
}

func perMessageHashes(messages []provider.Message) []string {
	hashes := make([]string, len(messages))
	for i, message := range messages {
		hashes[i] = messageHash(canonicalMessage(message))
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
	var data strings.Builder
	for _, message := range messages {
		canonical := canonicalMessage(message)
		data.WriteString(strconv.Itoa(len(canonical)))
		data.WriteByte(':')
		data.WriteString(canonical)
	}
	if data.Len() == 0 {
		return ""
	}
	return shortHash(data.String())
}

func longestCommonPrefixLen(previous, current []string) int {
	n := min(len(previous), len(current))
	for i := 0; i < n; i++ {
		if previous[i] != current[i] {
			return i
		}
	}
	return n
}

func messageHash(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:16])
}

func shortHash(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:4])
}
