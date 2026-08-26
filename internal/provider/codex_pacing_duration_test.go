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

// TestCodexPacingDurationMatched resolves a confound in TestCodexPacingSoak.
//
// That soak held request *count* equal across arms, which for a time-dependent
// effect is the wrong control: the unpaced arms finished in 13-47s while the
// paced arm ran 17-21 minutes. Every failure observed there began around 60
// seconds into the paced arm (first failures at request 11 and 13, both ~60-70s
// in), so the unpaced arms may simply have stopped before the onset rather than
// being immune to it.
//
// This holds wall-clock *duration* equal instead and reports the elapsed time of
// the first failure in each arm, which is the quantity that discriminates:
//
//   - Unpaced arm fails at ~60s too  -> the onset is a per-key session effect
//     and has nothing to do with pacing.
//   - Unpaced arm runs clean for the full duration while the paced arm fails
//     -> pacing itself is implicated, which would matter since the 4s default
//     was just removed.
//
// The unpaced arm runs FIRST here, reversing the soak's order. Both paced runs
// in the soak happened later in the session than any unpaced arm, so ordering
// and chronological drift were confounded there; flipping the order means a
// drift explanation now has to predict the opposite sign.
//
// Gated: STEINER_CODEX_PACING_DURATION=<seconds per arm>, e.g. 180.
func TestCodexPacingDurationMatched(t *testing.T) {
	raw := os.Getenv("STEINER_CODEX_PACING_DURATION")
	if raw == "" {
		t.Skip("set STEINER_CODEX_PACING_DURATION=<seconds per arm> (e.g. 180) to run the duration-matched comparison")
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 120 {
		t.Fatalf("STEINER_CODEX_PACING_DURATION must be an integer >= 120 (the observed onset is ~60s), got %q", raw)
	}
	armDuration := time.Duration(seconds) * time.Second

	ctx := context.Background()
	cfg := codexTestClientConfig(t)
	chatProvider, err := NewCodexResponses(cfg)
	if err != nil {
		t.Fatalf("construct http provider: %v", err)
	}
	basePrefix := codexPacingPrefix(t)

	type attempt struct {
		index     int
		elapsed   time.Duration
		ok        bool
		cacheRead int
		prompt    int
		rttMS     int64
		failure   string
	}

	type armResult struct {
		name     string
		gap      time.Duration
		attempts []attempt
		duration time.Duration
	}

	runArm := func(name string, gap time.Duration) armResult {
		cacheKey, err := NewPromptCacheKey()
		if err != nil {
			t.Fatalf("generate prompt cache key: %v", err)
		}

		messages := []Message{
			{Role: MessageRoleSystem, Content: basePrefix},
			{Role: MessageRoleUser, Content: "Reply with a single word."},
		}

		result := armResult{name: name, gap: gap}
		armStart := time.Now()
		lastStart := time.Now()

		for i := 1; time.Since(armStart) < armDuration; i++ {
			if wait := gap - time.Since(lastStart); wait > 0 {
				time.Sleep(wait)
			}
			lastStart = time.Now()
			elapsed := time.Since(armStart)

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
			rtt := time.Since(started).Milliseconds()

			if reqErr != nil {
				result.attempts = append(result.attempts, attempt{
					index: i, elapsed: elapsed, ok: false, rttMS: rtt, failure: reqErr.Error(),
				})
				continue
			}
			result.attempts = append(result.attempts, attempt{
				index: i, elapsed: elapsed, ok: true,
				cacheRead: cacheRead, prompt: promptTokens, rttMS: rtt,
			})
			messages = append(messages,
				Message{Role: MessageRoleAssistant, Content: reply},
				Message{Role: MessageRoleUser, Content: "Reply with a single word."})
		}

		result.duration = time.Since(armStart)
		return result
	}

	// Unpaced first, reversing the soak's order so a chronological-drift
	// explanation must predict the opposite sign to survive.
	arms := []armResult{runArm("unpaced", 0)}
	time.Sleep(60 * time.Second)
	arms = append(arms, runArm("paced-4s", 4*time.Second))

	t.Log("=== duration-matched pacing comparison ===")
	for _, arm := range arms {
		var ok, failed, cold int
		firstFailure := time.Duration(-1)
		firstCold := time.Duration(-1)
		for _, a := range arm.attempts {
			if !a.ok {
				failed++
				if firstFailure < 0 {
					firstFailure = a.elapsed
					t.Logf("%s: FIRST FAILURE at request #%d, t=%s: %s",
						arm.name, a.index, a.elapsed.Round(time.Second), truncateFailure(a.failure))
				}
				continue
			}
			ok++
			if a.cacheRead == 0 {
				cold++
				if firstCold < 0 {
					firstCold = a.elapsed
				}
			}
		}

		t.Logf("%-9s gap=%-3s over %s: %d ok, %d failed, %d cold",
			arm.name, arm.gap, arm.duration.Round(time.Second), ok, failed, cold)
		if firstFailure < 0 {
			t.Logf("%-9s no failures across the full %s", arm.name, arm.duration.Round(time.Second))
		}
		if firstCold >= 0 {
			t.Logf("%-9s first cold request at t=%s", arm.name, firstCold.Round(time.Second))
		}
		if ok == 0 {
			t.Errorf("%s produced no successful requests; the arm is unusable", arm.name)
		}
	}

	t.Log("discriminator: if the unpaced arm's first failure lands near the paced arm's,")
	t.Log("the ~60s onset is a per-key session effect and is unrelated to pacing")
}

// truncateFailure keeps arm summaries readable when a provider error carries a
// long body.
func truncateFailure(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= 120 {
		return msg
	}
	return msg[:120] + "…"
}
