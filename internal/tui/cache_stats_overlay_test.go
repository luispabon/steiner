package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

func TestFormatCacheStatsReportNilRecorder(t *testing.T) {
	t.Parallel()
	got := formatCacheStatsReport(nil)
	if !strings.Contains(got, "No cache statistics recorded yet") {
		t.Fatalf("nil recorder report = %q, want to contain 'No cache statistics recorded yet'", got)
	}
}

func TestFormatCacheStatsReportEmptyRecorder(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	rec := usagestats.New(func() time.Time { return time.Unix(1000, 0) })
	got := formatCacheStatsReport(rec)
	if !strings.Contains(got, "No cache statistics recorded yet") {
		t.Fatalf("empty recorder report = %q, want to contain 'No cache statistics recorded yet'", got)
	}
}

func TestFormatCacheStatsReportWithObservations(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(1000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record one observation with a cache hit
	rec.Record(usagestats.Observation{
		ProviderAlias:     "local",
		ProviderType:      "ollama",
		BackendModelID:    "llama2",
		PromptTokens:      100,
		CompletionTokens:  20,
		CacheReadTokens:   50,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Should contain provider and model
	if !strings.Contains(got, "local") {
		t.Fatalf("report = %q, want to contain provider 'local'", got)
	}
	if !strings.Contains(got, "llama2") {
		t.Fatalf("report = %q, want to contain model 'llama2'", got)
	}

	// Should contain hit rate percentage (cache hit = 50/(50+50+0) = 50%)
	if !strings.Contains(got, "50.0%") {
		t.Fatalf("report = %q, want to contain hit rate '50.0%%'", got)
	}

	// Should contain cached/total tokens
	if !strings.Contains(got, "50 / 100") {
		t.Fatalf("report = %q, want to contain '50 / 100'", got)
	}

	// Should contain section headers
	if !strings.Contains(got, "### Last hour") {
		t.Fatalf("report = %q, want to contain '### Last hour'", got)
	}
}

func TestFormatCacheStatsReportZeroHitRate(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(2000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record observation with only input tokens (PromptTokens - CacheCreateTokens - CacheReadTokens)
	// With 100 prompt tokens, 0 cache read, 0 cache create -> 100 input, 0 cached
	// Hit rate = 0 / (0 + 100 + 0) = 0%
	rec.Record(usagestats.Observation{
		ProviderAlias:     "zerotest",
		ProviderType:      "ollama",
		BackendModelID:    "zeromodel",
		PromptTokens:      100,
		CompletionTokens:  20,
		CacheReadTokens:   0,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Should contain 0% hit rate for the zerotest provider
	if !strings.Contains(got, "zerotest") {
		t.Fatalf("report = %q, want to contain provider 'zerotest'", got)
	}
	if !strings.Contains(got, "0.0%") {
		t.Fatalf("report = %q, want to contain hit rate '0.0%%'", got)
	}
}

func TestFormatCacheStatsReportUndefinedHitRate(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(3000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record observation with create tokens but no other input tokens.
	// The Recorder calculates InputTokens as PromptTokens - CacheReadTokens - CacheCreateTokens,
	// so InputTokens = 0 - 0 - 100 (clamped to 0).
	// Total = InputTokens + CacheReadTokens + CacheCreateTokens = 0 + 0 + 100 = 100
	// HitRate = 0 / 100 = 0% (not undefined).
	// To get undefined (total = 0), we need all zeros:
	rec.Record(usagestats.Observation{
		ProviderAlias:     "undefinedtest",
		ProviderType:      "ollama",
		BackendModelID:    "undefinedmodel",
		PromptTokens:      0,
		CompletionTokens:  20,
		CacheReadTokens:   0,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Should contain em-dash for undefined hit rate (when total input is zero).
	// Since we have only completion tokens (no input), hit rate is undefined.
	if !strings.Contains(got, "undefinedtest") {
		t.Fatalf("report = %q, want to contain provider 'undefinedtest'", got)
	}
	if !strings.Contains(got, " — ") {
		t.Fatalf("report = %q, want to contain '—' for undefined hit rate", got)
	}
}

func TestFormatCacheStatsReportDeterministicOrdering(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(1000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record observations in non-alphabetical order
	rec.Record(usagestats.Observation{
		ProviderAlias:     "zebra",
		ProviderType:      "ollama",
		BackendModelID:    "model-b",
		PromptTokens:      100,
		CompletionTokens:  20,
		CacheReadTokens:   0,
		CacheCreateTokens: 0,
		At:                now,
	})
	rec.Record(usagestats.Observation{
		ProviderAlias:     "alpha",
		ProviderType:      "ollama",
		BackendModelID:    "model-z",
		PromptTokens:      100,
		CompletionTokens:  20,
		CacheReadTokens:   0,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Find positions of provider names; alpha should come before zebra
	alphaPos := strings.Index(got, "alpha")
	zebraPos := strings.Index(got, "zebra")
	if alphaPos == -1 || zebraPos == -1 {
		t.Fatalf("report = %q, missing providers", got)
	}
	if alphaPos >= zebraPos {
		t.Fatalf("report providers not in alphabetical order: alpha at %d, zebra at %d", alphaPos, zebraPos)
	}
}

func TestFormatCacheStatsReportMultipleWindows(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(4000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record observation in current hour
	rec.Record(usagestats.Observation{
		ProviderAlias:     "local",
		ProviderType:      "ollama",
		BackendModelID:    "llama2",
		PromptTokens:      100,
		CompletionTokens:  20,
		CacheReadTokens:   0,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Should contain all three window headers
	if !strings.Contains(got, "### Last hour") {
		t.Fatalf("report missing 'Last hour' header")
	}
	if !strings.Contains(got, "### Last 24 hours") {
		t.Fatalf("report missing 'Last 24 hours' header")
	}
	if !strings.Contains(got, "### Last 7 days") {
		t.Fatalf("report missing 'Last 7 days' header")
	}

	// The observation is recent, so it will appear in all three windows (since they
	// are all still within the current hour). Check that data appears.
	if !strings.Contains(got, "llama2") {
		t.Fatalf("report should contain model name 'llama2'")
	}
}

func TestFormatCacheStatsReportSessionWithData(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(5000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record one observation in session
	rec.Record(usagestats.Observation{
		ProviderAlias:     "local",
		ProviderType:      "ollama",
		BackendModelID:    "llama2",
		PromptTokens:      100,
		CompletionTokens:  20,
		CacheReadTokens:   40,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Should contain "This session" header
	if !strings.Contains(got, "### This session") {
		t.Fatalf("report = %q, want to contain '### This session'", got)
	}

	// Session should show hit rate (40 / (60 + 40 + 0) = 40%)
	if !strings.Contains(got, "40.0%") {
		t.Fatalf("report = %q, want to contain session hit rate '40.0%%'", got)
	}

	// Session should show cached/total (40 / 100)
	if !strings.Contains(got, "40 / 100") {
		t.Fatalf("report = %q, want to contain session cached/total '40 / 100'", got)
	}
}

func TestFormatCacheStatsReportSessionHeaders(t *testing.T) {
	// Not parallel: usagestats.New persists to a store file derived from
	// XDG_STATE_HOME, which t.Setenv isolates per test. t.Setenv panics if
	// combined with t.Parallel().
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Unix(7000, 0)
	rec := usagestats.New(func() time.Time { return now })

	// Record an observation with a high cache hit rate
	rec.Record(usagestats.Observation{
		ProviderAlias:     "provider1",
		ProviderType:      "apitype",
		BackendModelID:    "model1",
		PromptTokens:      100,
		CompletionTokens:  30,
		CacheReadTokens:   60,
		CacheCreateTokens: 0,
		At:                now,
	})

	got := formatCacheStatsReport(rec)

	// Should contain "This session" header
	if !strings.Contains(got, "### This session") {
		t.Fatalf("report = %q, want to contain '### This session'", got)
	}

	// Session section should show the table
	if !strings.Contains(got, "| Provider | Model | Hit rate | Cached / Total |") {
		t.Fatalf("report = %q, want to contain table header in session section", got)
	}

	// Session should show hit rate (60 / (40 + 60 + 0) = 60%)
	if !strings.Contains(got, "60.0%") {
		t.Fatalf("report = %q, want to contain session hit rate '60.0%%'", got)
	}

	// Session should show cached/total (60 / 100)
	if !strings.Contains(got, "60 / 100") {
		t.Fatalf("report = %q, want to contain session cached/total '60 / 100'", got)
	}

	// Should still contain window headers
	if !strings.Contains(got, "### Last hour") {
		t.Fatalf("report = %q, want to contain 'Last hour' header", got)
	}
}
