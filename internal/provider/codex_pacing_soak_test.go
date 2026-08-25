package provider

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCodexPacingSoak closes the two gaps TestCodexPacingRateSweep left open
// before the 4s request pacing default was dropped to 0.
//
// The sweep established that a key sustaining 34.2 req/min for a full minute
// produced zero cold requests, against a documented claim that bursting past
// ~15 req/min scatters later turns onto cold shards. But it used a fixed
// prefix and strictly sequential requests. A real agentic run has neither: the
// prefix grows every turn, and delegated or parallel tool work can put several
// requests in flight at once, which raises instantaneous rate well above what
// sequential pacing can reach.
//
// Three arms, each on its own prompt_cache_key because the claimed overflow is
// per-key:
//
//	burst-growing  — no gap, prefix grows each turn (the regime pacing targets)
//	paced-growing  — 4s gap, prefix grows each turn (the control)
//	concurrent     — parallelism in flight, no gap (highest instantaneous rate)
//
// Pacing is harmful-if-removed only if the unpaced arms show cold requests the
// paced arm does not. Reporting is per-request with the index of the first cold
// request, because the claim is about *later* turns degrading — an arm-level
// average would smear that away.
//
// Gated: STEINER_CODEX_PACING_SOAK=<requests per arm>, e.g. 30.
// Optional: STEINER_CODEX_PACING_SOAK_PARALLEL=<n> (default 3).
// Optional: STEINER_CODEX_PACING_SOAK_ARMS=<comma-separated arm names> to run a
// subset, e.g. "paced-growing" to re-measure the control on its own. Arms run in
// the order listed here regardless of the order given.
func TestCodexPacingSoak(t *testing.T) {
	raw := os.Getenv("STEINER_CODEX_PACING_SOAK")
	if raw == "" {
		t.Skip("set STEINER_CODEX_PACING_SOAK=<requests per arm> (e.g. 30) to run the pacing soak")
	}
	perArm, err := strconv.Atoi(raw)
	if err != nil || perArm < 10 {
		t.Fatalf("STEINER_CODEX_PACING_SOAK must be an integer >= 10, got %q", raw)
	}

	parallel := 3
	if rawPar := os.Getenv("STEINER_CODEX_PACING_SOAK_PARALLEL"); rawPar != "" {
		parallel, err = strconv.Atoi(rawPar)
		if err != nil || parallel < 2 {
			t.Fatalf("STEINER_CODEX_PACING_SOAK_PARALLEL must be an integer >= 2, got %q", rawPar)
		}
	}

	ctx := context.Background()
	cfg := codexTestClientConfig(t)
	chatProvider, err := NewCodexResponses(cfg)
	if err != nil {
		t.Fatalf("construct http provider: %v", err)
	}

	basePrefix := codexPacingPrefix(t)

	type sample struct {
		index     int
		cacheRead int
		prompt    int
		rttMS     int64
	}

	send := func(arm string, cacheKey string, index int, messages []Message) (sample, string, bool) {
		request := ChatRequest{
			Model:          cfg.Model,
			PromptCacheKey: cacheKey,
			Messages:       messages,
		}

		started := time.Now()
		var cacheRead, promptTokens int
		var reply string
		stream, reqErr := chatProvider.StreamChatCompletion(ctx, request)
		if reqErr == nil {
			for chunk := range stream {
				reply += chunk.Delta.Content
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
			return sample{}, "", false
		}
		if promptTokens == 0 {
			t.Errorf("%s request %d: no prompt tokens reported", arm, index)
			return sample{}, "", false
		}
		return sample{index: index, cacheRead: cacheRead, prompt: promptTokens, rttMS: elapsed}, reply, true
	}

	newKey := func() string {
		key, err := NewPromptCacheKey()
		if err != nil {
			t.Fatalf("generate prompt cache key: %v", err)
		}
		return key
	}

	type armResult struct {
		name     string
		samples  []sample
		duration time.Duration
	}
	var results []armResult

	// growingArm walks a conversation that lengthens every turn, so each request
	// must match a longer cached prefix than the last — what a real run does.
	growingArm := func(name string, gap time.Duration) armResult {
		cacheKey := newKey()
		messages := []Message{
			{Role: MessageRoleSystem, Content: basePrefix},
			{Role: MessageRoleUser, Content: "Reply with a single word."},
		}

		if _, reply, ok := send(name, cacheKey, 0, messages); ok {
			messages = append(messages,
				Message{Role: MessageRoleAssistant, Content: reply},
				Message{Role: MessageRoleUser, Content: "Reply with a single word."})
		} else {
			t.Fatalf("%s: priming request failed", name)
		}

		result := armResult{name: name}
		armStart := time.Now()
		lastStart := time.Now()
		for i := 1; i <= perArm; i++ {
			if wait := gap - time.Since(lastStart); wait > 0 {
				time.Sleep(wait)
			}
			lastStart = time.Now()
			s, reply, ok := send(name, cacheKey, i, messages)
			if !ok {
				continue
			}
			result.samples = append(result.samples, s)
			messages = append(messages,
				Message{Role: MessageRoleAssistant, Content: reply},
				Message{Role: MessageRoleUser, Content: "Reply with a single word."})
		}
		result.duration = time.Since(armStart)
		return result
	}

	// concurrentArm keeps `parallel` requests in flight on one key, which drives
	// instantaneous rate higher than any sequential arm can.
	concurrentArm := func(name string) armResult {
		cacheKey := newKey()
		messages := []Message{
			{Role: MessageRoleSystem, Content: basePrefix},
			{Role: MessageRoleUser, Content: "Reply with a single word."},
		}
		if _, _, ok := send(name, cacheKey, 0, messages); !ok {
			t.Fatalf("%s: priming request failed", name)
		}

		var mu sync.Mutex
		result := armResult{name: name}
		armStart := time.Now()

		work := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < parallel; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range work {
					turn := []Message{
						{Role: MessageRoleSystem, Content: basePrefix},
						{Role: MessageRoleUser, Content: fmt.Sprintf("Reply with the single word: turn%d.", i)},
					}
					s, _, ok := send(name, cacheKey, i, turn)
					if !ok {
						continue
					}
					mu.Lock()
					result.samples = append(result.samples, s)
					mu.Unlock()
				}
			}()
		}
		for i := 1; i <= perArm; i++ {
			work <- i
		}
		close(work)
		wg.Wait()

		result.duration = time.Since(armStart)
		return result
	}

	// Unpaced arms run first: if bursting poisons a shard, residue carries into
	// the paced arm that follows, biasing against the claim under test.
	//
	// That ordering has a cost, seen on the first full run: the last arm absorbs
	// any late-session server-side slowdown, which cost paced-growing ten
	// requests to read-body timeouts and made it unusable as a control. Running
	// a single arm via STEINER_CODEX_PACING_SOAK_ARMS avoids that.
	selected := parseSoakArms(t, os.Getenv("STEINER_CODEX_PACING_SOAK_ARMS"))
	first := true
	runArm := func(name string, run func() armResult) {
		if !selected[name] {
			return
		}
		if !first {
			// Let any per-key rate window drain so the next arm's rate is its own.
			time.Sleep(30 * time.Second)
		}
		first = false
		results = append(results, run())
	}

	runArm("burst-growing", func() armResult { return growingArm("burst-growing", 0) })
	runArm("concurrent", func() armResult { return concurrentArm("concurrent") })
	runArm("paced-growing", func() armResult { return growingArm("paced-growing", 4*time.Second) })

	if len(results) == 0 {
		t.Fatalf("STEINER_CODEX_PACING_SOAK_ARMS selected no known arms")
	}

	t.Log("=== pacing soak: per-request cache reads ===")
	for _, arm := range results {
		for _, s := range arm.samples {
			rate := float64(s.cacheRead) / float64(s.prompt) * 100
			t.Logf("%-14s #%-3d cache_read=%-7d prompt=%-7d rate=%.1f%% rtt=%dms",
				arm.name, s.index, s.cacheRead, s.prompt, rate, s.rttMS)
		}
	}

	t.Log("=== summary ===")
	coldByArm := make(map[string]int, len(results))
	for _, arm := range results {
		var cached, prompt, cold int
		firstCold := -1
		for _, s := range arm.samples {
			cached += s.cacheRead
			prompt += s.prompt
			if s.cacheRead == 0 {
				cold++
				if firstCold < 0 || s.index < firstCold {
					firstCold = s.index
				}
			}
		}
		if prompt == 0 {
			t.Errorf("%s: no usable samples", arm.name)
			continue
		}
		coldByArm[arm.name] = cold
		firstColdLabel := "none"
		if firstCold >= 0 {
			firstColdLabel = strconv.Itoa(firstCold)
		}
		t.Logf("%-14s n=%-3d %.1f req/min over %s: hit rate=%.1f%% cold=%d first cold at #%s",
			arm.name, len(arm.samples), float64(len(arm.samples))/arm.duration.Minutes(),
			arm.duration.Round(time.Second), float64(cached)/float64(prompt)*100, cold, firstColdLabel)
	}

	// The removal is harmful only if going unpaced costs cold requests the paced
	// arm avoids. Anything else — including all three arms being equally cold —
	// is not an argument for pacing.
	paced, ok := coldByArm["paced-growing"]
	if !ok {
		t.Log("paced-growing did not run; reporting the unpaced arms only, no comparison made")
		return
	}
	for _, name := range []string{"burst-growing", "concurrent"} {
		if _, ran := coldByArm[name]; !ran {
			continue
		}
		if coldByArm[name] > paced {
			t.Errorf("%s had %d cold requests vs %d paced: pacing looks load-bearing after all, revisit dropping the default",
				name, coldByArm[name], paced)
		}
	}
}

// parseSoakArms selects which soak arms to run. Empty means all of them.
func parseSoakArms(t *testing.T, raw string) map[string]bool {
	t.Helper()
	known := []string{"burst-growing", "concurrent", "paced-growing"}
	selected := make(map[string]bool, len(known))
	if strings.TrimSpace(raw) == "" {
		for _, name := range known {
			selected[name] = true
		}
		return selected
	}
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if !slices.Contains(known, field) {
			t.Fatalf("unknown soak arm %q: want one of %v", field, known)
		}
		selected[field] = true
	}
	return selected
}
