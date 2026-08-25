package provider

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"
)

// TestCodexTransportLatency compares warm-turn round-trip time between the
// Codex WebSocket and HTTP transports.
//
// The WebSocket transport's original justification was cache affinity, which
// the idle-TTL finding removed (see cache_investigation_findings.md). Its
// remaining case is latency: no TCP/TLS handshake per request. That case is
// not obvious, because Go's HTTP client pools connections, so the HTTP arm may
// not be paying a handshake per request either.
//
// Both arms share one prefix and one prompt cache key, and requests alternate
// within each round so time-of-day drift cannot favour either. Arm order flips
// every round to cancel ordering bias. Gaps stay well under the ~5 minute cache
// TTL so every measured turn is warm.
//
// Limitation: round-trip time includes generation, which is variable. The
// prompt asks for a single word to keep that term small, and the report is a
// median over rounds, but a difference of a few tens of milliseconds is not
// resolvable this way.
//
// Gated: STEINER_CODEX_LATENCY=<rounds>, e.g. 8. Costs 2 requests per round
// plus 2 priming requests.
func TestCodexTransportLatency(t *testing.T) {
	raw := os.Getenv("STEINER_CODEX_LATENCY")
	if raw == "" {
		t.Skip("set STEINER_CODEX_LATENCY=<rounds> (e.g. 8) to run the transport latency comparison")
	}
	rounds, err := strconv.Atoi(raw)
	if err != nil || rounds < 1 {
		t.Fatalf("STEINER_CODEX_LATENCY must be a positive integer, got %q", raw)
	}

	gap := 30 * time.Second
	if rawGap := os.Getenv("STEINER_CODEX_LATENCY_GAP"); rawGap != "" {
		seconds, err := strconv.Atoi(rawGap)
		if err != nil || seconds < 0 {
			t.Fatalf("STEINER_CODEX_LATENCY_GAP must be a non-negative integer, got %q", rawGap)
		}
		gap = time.Duration(seconds) * time.Second
	}

	ctx := context.Background()
	cfg := codexTestClientConfig(t)

	wsProvider, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("construct websocket provider: %v", err)
	}
	httpProvider, err := NewCodexResponses(cfg)
	if err != nil {
		t.Fatalf("construct http provider: %v", err)
	}

	prefix := codexIdleProbePrefix(t)
	cacheKey, err := NewPromptCacheKey()
	if err != nil {
		t.Fatalf("generate prompt cache key: %v", err)
	}

	type sample struct {
		arm       string
		round     int
		rttMS     int64
		cacheRead int
		prompt    int
	}
	var samples []sample

	send := func(arm string, prov Provider, round int, question string) {
		request := ChatRequest{
			Model:          cfg.Model,
			PromptCacheKey: cacheKey,
			Messages: []Message{
				{Role: "system", Content: prefix},
				{Role: "user", Content: question},
			},
		}

		started := time.Now()
		var cacheRead, promptTokens int
		stream, reqErr := prov.StreamChatCompletion(ctx, request)
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

		if reqErr != nil {
			t.Errorf("%s round %d: request failed: %v", arm, round, reqErr)
			return
		}
		if promptTokens == 0 {
			t.Errorf("%s round %d: no prompt tokens reported, no usable measurement", arm, round)
			return
		}
		samples = append(samples, sample{arm: arm, round: round, rttMS: elapsed, cacheRead: cacheRead, prompt: promptTokens})
	}

	// Prime both arms: establish the connections and warm the shared cache
	// entry, so round 1 is not measuring first-request effects.
	send("websocket-prime", wsProvider, 0, "Reply with the single word: ready.")
	send("http-prime", httpProvider, 0, "Reply with the single word: ready.")
	samples = nil

	for round := 1; round <= rounds; round++ {
		order := []struct {
			arm  string
			prov Provider
		}{{"websocket", wsProvider}, {"http", httpProvider}}
		if round%2 == 0 {
			order[0], order[1] = order[1], order[0]
		}

		for _, entry := range order {
			time.Sleep(gap)
			send(entry.arm, entry.prov, round, fmt.Sprintf("Reply with the single word: round%d.", round))
		}
	}

	t.Log("=== transport latency samples ===")
	for _, s := range samples {
		t.Logf("round %-3d %-10s rtt=%-6dms prompt=%-7d cache_read=%d", s.round, s.arm, s.rttMS, s.prompt, s.cacheRead)
	}

	report := func(arm string) []int64 {
		var values []int64
		for _, s := range samples {
			if s.arm == arm {
				values = append(values, s.rttMS)
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		return values
	}

	wsValues, httpValues := report("websocket"), report("http")
	if len(wsValues) == 0 || len(httpValues) == 0 {
		t.Fatal("no usable samples for one or both arms")
	}

	t.Log("=== summary ===")
	t.Logf("websocket: n=%d median=%dms mean=%dms min=%dms max=%dms",
		len(wsValues), median(wsValues), meanInt(wsValues), wsValues[0], wsValues[len(wsValues)-1])
	t.Logf("http:      n=%d median=%dms mean=%dms min=%dms max=%dms",
		len(httpValues), median(httpValues), meanInt(httpValues), httpValues[0], httpValues[len(httpValues)-1])
	t.Logf("median delta (http - websocket) = %dms", median(httpValues)-median(wsValues))

	// Every measured turn must be warm, or the comparison is measuring cache
	// misses rather than transport.
	for _, s := range samples {
		if s.cacheRead == 0 {
			t.Errorf("round %d %s: cold turn (cache_read=0) — gap exceeded the cache TTL, comparison invalid", s.round, s.arm)
		}
	}
}

func median(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func meanInt(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var total int64
	for _, v := range values {
		total += v
	}
	return total / int64(len(values))
}
