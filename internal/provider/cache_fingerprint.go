package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// WireCacheDiagnostics contains opaque diagnostics derived from a real HTTP
// request payload and its headers. It carries no request content or keys.
type WireCacheDiagnostics struct {
	CacheablePrefixHash string
	SharedPrefixHash    string
	BodyKeyHash         string
	SessionIDHash       string
	ThreadIDHash        string
	OriginatorHash      string
	AffinityKeysMatch   bool
	Stream              bool
	HasTools            bool
	ToolCount           int
	HasReasoning        bool
	Store               bool
}

// CacheFingerprinter is an optional provider capability for wire-boundary cache
// diagnostics. Providers without HTTP cache affinity may omit it.
type CacheFingerprinter interface {
	CacheFingerprint(context.Context, ChatRequest, bool, int, int) (WireCacheDiagnostics, error)
}

// CacheFingerprint derives diagnostics using the same Wire payload and request
// construction paths used for execution. It never sends a request.
func (c *Client) CacheFingerprint(ctx context.Context, request ChatRequest, stream bool, cacheableMessages, sharedMessages int) (WireCacheDiagnostics, error) {
	if c == nil || c.wire == nil {
		return WireCacheDiagnostics{}, fmt.Errorf("provider is not initialized")
	}
	fingerprinter, ok := c.wire.(interface {
		cacheFingerprint(context.Context, ChatRequest, bool, int, int) (WireCacheDiagnostics, error)
	})
	if !ok {
		return WireCacheDiagnostics{}, fmt.Errorf("wire cache fingerprint is unavailable")
	}
	return fingerprinter.cacheFingerprint(ctx, request, stream, cacheableMessages, sharedMessages)
}

func hashOpaque(domain string, data []byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func hashString(domain, value string) string {
	if value == "" {
		return ""
	}
	return hashOpaque(domain, []byte(value))
}

func truncateRequestMessages(request ChatRequest, count int) ChatRequest {
	request.Messages = CloneMessages(request.Messages)
	if count < 0 {
		count = 0
	}
	if count < len(request.Messages) {
		request.Messages = request.Messages[:count]
	}
	return request
}

func (w *responsesWire) cacheFingerprint(ctx context.Context, request ChatRequest, stream bool, cacheableMessages, sharedMessages int) (WireCacheDiagnostics, error) {
	cacheable := truncateRequestMessages(request, cacheableMessages)
	shared := truncateRequestMessages(request, sharedMessages)
	cacheableBody, err := w.Payload(cacheable, stream)
	if err != nil {
		return WireCacheDiagnostics{}, err
	}
	sharedBody, err := w.Payload(shared, stream)
	if err != nil {
		return WireCacheDiagnostics{}, err
	}
	httpRequest, err := w.HTTPRequest(ctx, request, cacheableBody, stream)
	if err != nil {
		return WireCacheDiagnostics{}, err
	}
	bodyKey := request.PromptCacheKey
	sessionID := httpRequest.Header.Get("session-id")
	threadID := httpRequest.Header.Get("thread-id")
	originator := httpRequest.Header.Get("originator")
	return WireCacheDiagnostics{
		CacheablePrefixHash: hashOpaque("steiner.codex.wire-prefix.v1", cacheableBody),
		SharedPrefixHash:    hashOpaque("steiner.codex.wire-prefix.v1", sharedBody),
		BodyKeyHash:         hashString("steiner.codex.body-key.v1", bodyKey),
		SessionIDHash:       hashString("steiner.codex.header.session-id.v1", sessionID),
		ThreadIDHash:        hashString("steiner.codex.header.thread-id.v1", threadID),
		OriginatorHash:      hashString("steiner.codex.header.originator.v1", originator),
		AffinityKeysMatch:   bodyKey != "" && bodyKey == sessionID && bodyKey == threadID,
		Stream:              stream,
		HasTools:            len(request.Tools) > 0,
		ToolCount:           len(request.Tools),
		HasReasoning:        request.Reasoning != nil,
		Store:               responseStore(cacheableBody),
	}, nil
}

func responseStore(body []byte) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	var store bool
	_ = json.Unmarshal(payload["store"], &store)
	return store
}

var _ CacheFingerprinter = (*Client)(nil)
