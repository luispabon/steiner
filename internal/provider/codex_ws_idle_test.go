package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/oauth"
	"github.com/luispabon/steiner/internal/usagestats"
)

// TestCodexWSIdleDeath probes whether a Codex WebSocket connection survives an
// idle gap, and what an idle-induced reconnect costs in cache hits.
//
// Interactive sessions have think-time between turns; a headless exec run does
// not. codexWSProvider treats a non-nil conn as a live conn and only discovers
// a server-closed socket on the next write or read, where it silently
// reconnects — so an idle death is invisible from request results alone. This
// probe makes it visible by pointing the telemetry writer at a temp file and
// reading back the recorded dial/reconnect events.
//
// Gated: set STEINER_CODEX_WS_IDLE to a comma-separated list of idle gaps in
// minutes, e.g. STEINER_CODEX_WS_IDLE=1,5,15,30. Each gap costs one request,
// preceded by one priming request. Run it in the background: the wall time is
// the sum of the gaps.
func TestCodexWSIdleDeath(t *testing.T) {
	gaps := parseIdleGaps(t, os.Getenv("STEINER_CODEX_WS_IDLE"))
	if len(gaps) == 0 {
		t.Skip("set STEINER_CODEX_WS_IDLE=1,5,15,30 (minutes) to run the idle-death probe")
	}

	telemetryPath := filepath.Join(t.TempDir(), "idle-probe.jsonl")
	t.Setenv(usagestats.TelemetryEnvVar, telemetryPath)
	t.Setenv(usagestats.TelemetryRunEnvVar, "idle-probe")

	ctx := context.Background()
	cfg := codexTestClientConfig(t)

	// The HTTP arm is the control for the cache-TTL confound: it has no socket
	// to lose, so if it also reads 0% after the same gap, the collapse is the
	// backend's prompt-cache expiry rather than the WebSocket reconnect.
	transport := os.Getenv("STEINER_CODEX_WS_IDLE_TRANSPORT")
	if transport == "" {
		transport = "websocket"
	}

	var chatProvider Provider
	var err error
	switch transport {
	case "websocket":
		chatProvider, err = NewCodexResponsesWS(cfg)
	case "http":
		chatProvider, err = NewCodexResponses(cfg)
	default:
		t.Fatalf("unknown transport %q: want websocket or http", transport)
	}
	if err != nil {
		t.Fatalf("construct %s provider: %v", transport, err)
	}
	t.Logf("transport under test: %s", transport)

	prefix := codexIdleProbePrefix(t)
	cacheKey, err := NewPromptCacheKey()
	if err != nil {
		t.Fatalf("generate prompt cache key: %v", err)
	}

	type turnResult struct {
		label       string
		gap         time.Duration
		cacheRead   int
		promptTok   int
		reconnects  int
		requestErr  error
		roundTripMS int64
	}

	var results []turnResult
	priorWSEvents := 0

	send := func(label string, gap time.Duration, question string) {
		request := ChatRequest{
			Model:          cfg.Model,
			PromptCacheKey: cacheKey,
			Messages: []Message{
				{Role: "system", Content: prefix},
				{Role: "user", Content: question},
			},
		}

		// Both arms stream: the Codex HTTP endpoint rejects non-streaming
		// requests outright ("Stream must be set to true"), and streaming is
		// also what steiner's interactive path actually uses.
		started := time.Now()
		var cacheRead, promptTokens int
		stream, reqErr := chatProvider.StreamChatCompletion(ctx, request)
		if reqErr == nil {
			for chunk := range stream {
				if chunk.Usage != nil {
					cacheRead = chunk.Usage.CacheReadInputTokens
					promptTokens = chunk.Usage.PromptTokens
				}
				if chunk.Error != "" && reqErr == nil {
					reqErr = fmt.Errorf("%s", chunk.Error)
				}
			}
		}
		elapsed := time.Since(started).Milliseconds()

		events := readWSTelemetryLinesIfPresent(t, telemetryPath)
		reconnects := 0
		for _, event := range events[min(priorWSEvents, len(events)):] {
			if event.Event == wsTelemetryEventReconnect || event.Event == wsTelemetryEventDial {
				reconnects++
			}
		}
		priorWSEvents = len(events)

		results = append(results, turnResult{
			label:       label,
			gap:         gap,
			cacheRead:   cacheRead,
			promptTok:   promptTokens,
			reconnects:  reconnects,
			requestErr:  reqErr,
			roundTripMS: elapsed,
		})
	}

	send("prime", 0, "Reply with the single word: ready.")

	for i, gap := range gaps {
		t.Logf("idling %s before turn %d…", gap, i+2)
		time.Sleep(gap)
		send(fmt.Sprintf("after %s idle", gap), gap, fmt.Sprintf("Reply with the single word: turn%d.", i+2))
	}

	t.Log("=== idle-death probe results ===")
	for _, r := range results {
		status := "ok"
		if r.requestErr != nil {
			status = "ERROR: " + r.requestErr.Error()
		}
		// The prime turn's dial is expected; any connection event on a later
		// turn means the idle gap killed the socket.
		t.Logf("%-18s gap=%-6s prompt=%-7d cache_read=%-7d conn_events=%d rtt=%dms %s",
			r.label, r.gap, r.promptTok, r.cacheRead, r.reconnects, r.roundTripMS, status)
	}

	for _, r := range results {
		if r.requestErr != nil {
			t.Errorf("%s: request failed, no usable measurement: %v", r.label, r.requestErr)
		}
		if r.promptTok == 0 {
			t.Errorf("%s: no prompt tokens reported, no usable measurement", r.label)
		}
	}

	for _, r := range results[1:] {
		if transport == "websocket" && r.reconnects > 0 {
			t.Errorf("connection did not survive a %s idle gap: %d connection event(s) recorded", r.gap, r.reconnects)
		}
	}
	if len(results) > 1 && results[1].promptTok > 0 && results[1].cacheRead == 0 && transport == "websocket" {
		t.Errorf("no cache read on the turn after the first idle gap (prompt=%d): prefix may be below the cache threshold, making this probe uninterpretable", results[1].promptTok)
	}
}

// readWSTelemetryLinesIfPresent reads connection events, tolerating a missing
// file: the HTTP control arm records none, so the file is never created.
func readWSTelemetryLinesIfPresent(t *testing.T, path string) []wsTelemetryLine {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return readWSTelemetryLines(t, path)
}

func parseIdleGaps(t *testing.T, raw string) []time.Duration {
	t.Helper()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var gaps []time.Duration
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		minutes, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("parse idle gap %q: %v", field, err)
		}
		gaps = append(gaps, time.Duration(minutes)*time.Minute)
	}
	return gaps
}

// codexIdleProbePrefix builds a large, byte-stable system prefix from this
// repository's own source, so every turn shares an identical cacheable prefix
// comfortably above Codex's ~1024-token minimum. Arm C of the original
// measurement read a hard 0% with a ~1750-token prefix, so this deliberately
// aims an order of magnitude higher.
func codexIdleProbePrefix(t *testing.T) string {
	t.Helper()
	sources := []string{
		"codex_responses_stream.go",
		"codex_responses_wire.go",
		"codex_responses_ws.go",
		"openai_compat.go",
		"openai_wire.go",
		"anthropic_wire.go",
		"client.go",
		"retry.go",
	}
	var b strings.Builder
	b.WriteString("You are a code review assistant. Reference material follows.\n\n")
	for _, name := range sources {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read prefix source %s: %v", name, err)
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n", name, data)
	}
	return b.String()
}

// codexTestClientConfig builds a ClientConfig from the live OAuth token,
// mirroring cmd/steiner/runtime_build.go's newCodexProvider.
func codexTestClientConfig(t *testing.T) ClientConfig {
	t.Helper()

	tokenPath, err := oauth.DefaultTokenPath()
	if err != nil {
		t.Fatalf("resolve token path: %v", err)
	}
	store := oauth.NewTokenStore(tokenPath)
	token, err := store.Load()
	if err != nil {
		t.Fatalf("load OAuth token: %v", err)
	}
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		t.Fatalf("refresh codex token: %v", err)
	}

	model := os.Getenv("STEINER_CODEX_WS_MODEL")
	if model == "" {
		model = "gpt-5.6-luna"
	}

	cfg := ClientConfig{
		Headers: make(map[string]string),
		Model:   model,
		Timeout: 120 * time.Second,
	}
	if apiKey := oauth.TokenOpenAIAPIKey(token); apiKey != "" {
		cfg.APIKey = apiKey
		cfg.BaseURL = "https://api.openai.com/v1"
		return cfg
	}

	accountID := oauth.TokenChatGPTAccountID(token)
	if accountID == "" {
		t.Fatalf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
	}
	cfg.APIKey = token.AccessToken
	cfg.BaseURL = "https://chatgpt.com/backend-api/codex"
	cfg.Headers["ChatGPT-Account-ID"] = accountID
	return cfg
}
