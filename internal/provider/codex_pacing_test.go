package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestCodexPacingCacheEffect tests whether the 4-second minimum request
// interval (config.DefaultCodexMinRequestInterval) actually improves cache hit
// rates, as docs/configuration.md claims: "Codex limits cache reuse when too
// many requests from the same key land on cold cache shards. This interval
// paces rapid bursts to reduce cold-shard cache misses."
//
// That claim is the same shape as the WebSocket cache-affinity hypothesis that
// the idle-TTL measurement disproved: a plausible mechanism asserted in a
// comment, never measured against a control. It costs 4 seconds on every
// request in burst/exec mode, so it is worth checking.
//
// Both arms send an identical prefix under one prompt cache key, back to back,
// differing only in the gap between requests. Well inside the ~5 minute cache
// TTL, so every request should be warm regardless of pacing.
//
// Gated: STEINER_CODEX_PACING=<requests per arm>, e.g. 5.
func TestCodexPacingCacheEffect(t *testing.T) {
	raw := os.Getenv("STEINER_CODEX_PACING")
	if raw == "" {
		t.Skip("set STEINER_CODEX_PACING=<requests per arm> (e.g. 5) to test the pacing cache claim")
	}
	perArm, err := strconv.Atoi(raw)
	if err != nil || perArm < 2 {
		t.Fatalf("STEINER_CODEX_PACING must be an integer >= 2, got %q", raw)
	}

	ctx := context.Background()
	cfg := codexTestClientConfig(t)
	provider, err := NewCodexResponses(cfg)
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
		index     int
		cacheRead int
		prompt    int
		rttMS     int64
	}

	send := func(arm string, index int) (sample, bool) {
		request := ChatRequest{
			Model:          cfg.Model,
			PromptCacheKey: cacheKey,
			Messages: []Message{
				{Role: "system", Content: prefix},
				{Role: "user", Content: fmt.Sprintf("Reply with the single word: %s%d.", arm, index)},
			},
		}

		started := time.Now()
		var cacheRead, promptTokens int
		stream, reqErr := provider.StreamChatCompletion(ctx, request)
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
			t.Errorf("%s request %d failed: %v", arm, index, reqErr)
			return sample{}, false
		}
		if promptTokens == 0 {
			t.Errorf("%s request %d: no prompt tokens reported", arm, index)
			return sample{}, false
		}
		return sample{arm: arm, index: index, cacheRead: cacheRead, prompt: promptTokens, rttMS: elapsed}, true
	}

	// Prime, so neither arm pays the first-write miss.
	if _, ok := send("prime", 0); !ok {
		t.Fatal("priming request failed; no usable measurement")
	}

	var samples []sample
	runArm := func(arm string, gap time.Duration) {
		for i := 1; i <= perArm; i++ {
			if gap > 0 {
				time.Sleep(gap)
			}
			if s, ok := send(arm, i); ok {
				samples = append(samples, s)
			}
		}
	}

	// Unpaced first: if bursting poisons shards, the paced arm that follows
	// would inherit the damage, which biases against the claim being tested
	// rather than for it.
	runArm("burst", 0)
	time.Sleep(10 * time.Second)
	runArm("paced", 4*time.Second)

	t.Log("=== pacing cache effect ===")
	for _, s := range samples {
		rate := float64(s.cacheRead) / float64(s.prompt) * 100
		t.Logf("%-6s #%d cache_read=%-7d prompt=%-7d rate=%.1f%% rtt=%dms", s.arm, s.index, s.cacheRead, s.prompt, rate, s.rttMS)
	}

	summarise := func(arm string) (int, int, int) {
		var cached, prompt, cold int
		for _, s := range samples {
			if s.arm != arm {
				continue
			}
			cached += s.cacheRead
			prompt += s.prompt
			if s.cacheRead == 0 {
				cold++
			}
		}
		return cached, prompt, cold
	}

	burstCached, burstPrompt, burstCold := summarise("burst")
	pacedCached, pacedPrompt, pacedCold := summarise("paced")

	t.Log("=== summary ===")
	if burstPrompt > 0 {
		t.Logf("burst (0s gap): hit rate=%.1f%% cold requests=%d/%d",
			float64(burstCached)/float64(burstPrompt)*100, burstCold, perArm)
	}
	if pacedPrompt > 0 {
		t.Logf("paced (4s gap): hit rate=%.1f%% cold requests=%d/%d",
			float64(pacedCached)/float64(pacedPrompt)*100, pacedCold, perArm)
	}
	t.Logf("pacing costs 4s per request; it is justified only if the burst arm is measurably worse")
}
