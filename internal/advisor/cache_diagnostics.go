package advisor

import (
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
}

func (s *handlerState) emitCacheDiagnostic(writer *diagnostics.Writer, model provider.ResolvedModel, usage *provider.UsageStats, messages []provider.Message) {
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
	}})
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
