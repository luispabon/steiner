package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCodexPacingRateSweep re-tests the 4-second request pacing
// (config.DefaultCodexMinRequestInterval) against the specific mechanism its
// documentation claims, which an earlier test (TestCodexPacingCacheEffect)
// failed to load.
//
// docs/cache-stats.md states: "OpenAI still load-balances a key away from its
// warm shard when a single key bursts past roughly 15 requests/minute […]
// steiner naturally sends turns only ~1.5s apart, which is enough to trip that
// overflow and scatter later turns onto cold shards."
//
// That claim has three properties the earlier test did not reproduce:
//
//  1. It is about a sustained *rate*, not a single burst. Five back-to-back
//     requests never hold a rate over a minute-long window, so a load balancer
//     reacting to a per-minute counter would never see them.
//  2. It is per prompt-cache key ("a single key bursts past"). Sharing one key
//     across arms makes any damage structural rather than merely sequential.
//  3. It predicts a *transition*: "later turns" scatter. An aggregate hit rate
//     over the whole arm smears that away; the signal is the index of the first
//     cold request.
//
// So this sweeps request rate across arms, gives each arm its own cache key,
// and reports per-request cache reads plus the first cold index. 4s is exactly
// 15 requests/minute — the threshold the doc names — which is itself a hint
// that the 4s default was reverse-engineered from it.
//
// Gated: STEINER_CODEX_PACING_RATE=<requests per arm>, e.g. 25. Arms are
// selected with STEINER_CODEX_PACING_GAPS (comma-separated seconds, default
// "1.5,4"). Each arm costs one priming request plus <requests per arm>.
func TestCodexPacingRateSweep(t *testing.T) {
	raw := os.Getenv("STEINER_CODEX_PACING_RATE")
	if raw == "" {
		t.Skip("set STEINER_CODEX_PACING_RATE=<requests per arm> (e.g. 25) to run the pacing rate sweep")
	}
	perArm, err := strconv.Atoi(raw)
	if err != nil || perArm < 10 {
		t.Fatalf("STEINER_CODEX_PACING_RATE must be an integer >= 10 (the claimed mechanism needs a sustained rate), got %q", raw)
	}

	gaps := parsePacingGaps(t, os.Getenv("STEINER_CODEX_PACING_GAPS"))

	ctx := context.Background()
	cfg := codexTestClientConfig(t)
	provider, err := NewCodexResponses(cfg)
	if err != nil {
		t.Fatalf("construct http provider: %v", err)
	}

	// A smaller prefix than the idle probe's: the mechanism under test is shard
	// routing, not prefix size, so it only needs to clear Codex's caching
	// minimum. This keeps a 2x25-request sweep affordable.
	prefix := codexPacingPrefix(t)

	type sample struct {
		gap       time.Duration
		index     int
		cacheRead int
		prompt    int
		rttMS     int64
	}

	send := func(arm string, gap time.Duration, cacheKey string, index int) (sample, bool) {
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
		return sample{gap: gap, index: index, cacheRead: cacheRead, prompt: promptTokens, rttMS: elapsed}, true
	}

	// Cacheability check, two requests: if this prefix does not produce a cache
	// read on its own immediate repeat, it is below whatever threshold the
	// backend applies and every arm below would report a meaningless 0%. The
	// original Arm C measured a hard 0% for exactly this reason.
	probeKey, err := NewPromptCacheKey()
	if err != nil {
		t.Fatalf("generate prompt cache key: %v", err)
	}
	if _, ok := send("cacheable", 0, probeKey, 0); !ok {
		t.Fatal("cacheability check: first request failed")
	}
	check, ok := send("cacheable", 0, probeKey, 1)
	if !ok {
		t.Fatal("cacheability check: second request failed")
	}
	if check.cacheRead == 0 {
		t.Fatalf("cacheability check: prefix of %d prompt tokens read 0 cached tokens on immediate repeat; "+
			"it is below the backend's caching threshold, so the sweep below would measure nothing. "+
			"Enlarge codexPacingPrefix and re-run.", check.prompt)
	}
	t.Logf("cacheability check: prefix=%d prompt tokens, repeat read %d cached tokens — usable",
		check.prompt, check.cacheRead)

	type armResult struct {
		gap      time.Duration
		samples  []sample
		duration time.Duration
	}
	var results []armResult

	// Arms run fastest-first. If bursting really does poison a shard, any
	// residue carries into the slower arms that follow, which biases against
	// the claim under test rather than for it.
	for _, gap := range gaps {
		arm := fmt.Sprintf("gap%s", gap)

		// Each arm gets its own key: the claimed overflow is per-key, so a
		// shared key would let one arm's scattering contaminate the next.
		cacheKey, err := NewPromptCacheKey()
		if err != nil {
			t.Fatalf("generate prompt cache key: %v", err)
		}

		// Prime, so the arm's first measured request is not paying the
		// unavoidable first-write miss.
		if _, ok := send(arm, gap, cacheKey, 0); !ok {
			t.Fatalf("%s: priming request failed; arm has no usable measurement", arm)
		}

		result := armResult{gap: gap}
		armStart := time.Now()
		lastStart := time.Now()
		for i := 1; i <= perArm; i++ {
			// Pace on request *start* times, matching Client.pace, so the arm
			// holds the intended requests-per-minute rather than drifting with
			// round-trip time.
			if wait := gap - time.Since(lastStart); wait > 0 {
				time.Sleep(wait)
			}
			lastStart = time.Now()
			if s, ok := send(arm, gap, cacheKey, i); ok {
				result.samples = append(result.samples, s)
			}
		}
		result.duration = time.Since(armStart)
		results = append(results, result)

		// Let any per-key rate window drain before the next arm starts, so the
		// next arm's rate is its own.
		time.Sleep(30 * time.Second)
	}

	t.Log("=== pacing rate sweep: per-request cache reads ===")
	for _, arm := range results {
		for _, s := range arm.samples {
			rate := float64(s.cacheRead) / float64(s.prompt) * 100
			t.Logf("gap=%-5s #%-3d cache_read=%-7d prompt=%-7d rate=%.1f%% rtt=%dms",
				arm.gap, s.index, s.cacheRead, s.prompt, rate, s.rttMS)
		}
	}

	t.Log("=== summary ===")
	for _, arm := range results {
		var cached, prompt, cold int
		firstCold := -1
		for _, s := range arm.samples {
			cached += s.cacheRead
			prompt += s.prompt
			if s.cacheRead == 0 {
				cold++
				if firstCold < 0 {
					firstCold = s.index
				}
			}
		}
		if prompt == 0 {
			t.Errorf("gap=%s: no usable samples", arm.gap)
			continue
		}
		perMinute := float64(len(arm.samples)) / arm.duration.Minutes()
		firstColdLabel := "none"
		if firstCold >= 0 {
			firstColdLabel = strconv.Itoa(firstCold)
		}
		t.Logf("gap=%-5s n=%-3d sustained=%.1f req/min over %s: hit rate=%.1f%% cold=%d first cold at #%s",
			arm.gap, len(arm.samples), perMinute, arm.duration.Round(time.Second),
			float64(cached)/float64(prompt)*100, cold, firstColdLabel)
	}
	t.Log("the doc claims bursting past ~15 req/min scatters later turns onto cold shards;")
	t.Log("that predicts the fast arms show cold requests appearing partway through, and the 4s arm does not")
}

// parsePacingGaps parses a comma-separated list of inter-request gaps in
// seconds. The default sweep is 1.5s (~40 req/min, what steiner actually
// produces in exec mode per docs/cache-stats.md) and 4s (~15 req/min, the
// current default, sitting exactly on the claimed threshold).
func parsePacingGaps(t *testing.T, raw string) []time.Duration {
	t.Helper()
	if strings.TrimSpace(raw) == "" {
		raw = "1.5,4"
	}
	var gaps []time.Duration
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		seconds, err := strconv.ParseFloat(field, 64)
		if err != nil || seconds < 0 {
			t.Fatalf("STEINER_CODEX_PACING_GAPS entry %q must be a non-negative number of seconds", field)
		}
		gaps = append(gaps, time.Duration(seconds*float64(time.Second)))
	}
	if len(gaps) == 0 {
		t.Fatal("STEINER_CODEX_PACING_GAPS produced no arms")
	}
	return gaps
}

// codexPacingPrefix builds a byte-stable system prefix large enough to be
// cacheable but far smaller than codexIdleProbePrefix's ~17k tokens, since this
// sweep sends an order of magnitude more requests.
func codexPacingPrefix(t *testing.T) string {
	t.Helper()
	sources := []string{
		"client.go",
		"retry.go",
		"codex_responses_stream.go",
		"openai_compat.go",
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
