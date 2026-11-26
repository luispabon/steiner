package provider

// Simulated cache accounting for the requests the agent segmented-compaction
// fixture actually sent. The goldens hold those requests, main and summarizer
// calls interleaved in send order, so replaying them in that order reproduces
// how the two modes share one production cache.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	// capturedSimFixtureDir is the captured-request golden directory written by
	// the agent segmented compaction fixture.
	capturedSimFixtureDir = "../agent/testdata/segmented_compaction"
	// capturedSimSystemPreambleFile holds the session system preamble the agent
	// fixture stores once instead of repeating it in every captured request.
	capturedSimSystemPreambleFile = "system_preamble.txt"
	// capturedSimSystemPreamblePlaceholder is the marker the agent fixture writes
	// in place of that preamble; keep it in sync with the agent fixture.
	capturedSimSystemPreamblePlaceholder = "<<system_preamble.txt>>"
	// capturedSimCostTolerancePercent is the live bench's default cost band: the
	// offline proxy cannot resolve small differences such as per-segment framing
	// overhead, and the live bench decides within the same band.
	capturedSimCostTolerancePercent = 5
)

// capturedSimEntry mirrors one captured-request golden entry. CacheBoundary is
// json:"-", so the message indices are the only record of the boundary flags.
type capturedSimEntry struct {
	Request              ChatRequest `json:"request"`
	CacheBoundaryIndices []int       `json:"cache_boundary_indices"`
}

// capturedSimTotals is one mode's aggregated simulated run.
type capturedSimTotals struct {
	costProxy float64
	cached    int
	written   int
	uncached  int
}

// capturedSimLoadRequests decodes one captured-request golden, expands the
// shared session preamble stored beside it, and restores the CacheBoundary
// flags the JSON encoding drops. It also returns how many boundaries it
// restored.
func capturedSimLoadRequests(t *testing.T, name string) ([]ChatRequest, int) {
	t.Helper()
	preamblePath := filepath.Join(capturedSimFixtureDir, capturedSimSystemPreambleFile)
	preamble, err := os.ReadFile(preamblePath)
	if err != nil {
		t.Fatalf("read captured system preamble %s: %v", preamblePath, err)
	}
	if len(preamble) == 0 {
		t.Fatalf("captured system preamble %s is empty", preamblePath)
	}
	path := filepath.Join(capturedSimFixtureDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read captured requests %s: %v", path, err)
	}
	var entries []capturedSimEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode captured requests %s: %v", path, err)
	}
	if len(entries) == 0 {
		t.Fatalf("captured requests %s is empty", path)
	}
	requests := make([]ChatRequest, 0, len(entries))
	boundaries := 0
	for i, entry := range entries {
		replaced := 0
		for j := range entry.Request.Messages {
			message := &entry.Request.Messages[j]
			if message.Role != MessageRoleSystem {
				continue
			}
			if message.Content != capturedSimSystemPreamblePlaceholder {
				t.Fatalf("captured requests %s entry %d: system message %d does not hold the stored preamble placeholder", path, i, j)
			}
			message.Content = string(preamble)
			replaced++
		}
		if replaced == 0 {
			t.Fatalf("captured requests %s entry %d: request carries no system preamble", path, i)
		}
		for _, idx := range entry.CacheBoundaryIndices {
			if idx < 0 || idx >= len(entry.Request.Messages) {
				t.Fatalf("captured requests %s entry %d: cache boundary index %d out of range for %d messages", path, i, idx, len(entry.Request.Messages))
			}
			entry.Request.Messages[idx].CacheBoundary = true
			boundaries++
		}
		requests = append(requests, entry.Request)
	}
	return requests, boundaries
}

// capturedSimClearBoundaries returns the requests with every CacheBoundary
// flag removed, so the same sequence can be replayed without the segment
// boundary breakpoint.
func capturedSimClearBoundaries(reqs []ChatRequest) []ChatRequest {
	cleared := make([]ChatRequest, len(reqs))
	for i, req := range reqs {
		messages := make([]Message, len(req.Messages))
		copy(messages, req.Messages)
		for j := range messages {
			messages[j].CacheBoundary = false
		}
		req.Messages = messages
		cleared[i] = req
	}
	return cleared
}

// capturedSimSum totals one mode's simulated run.
func capturedSimSum(results []simResult) capturedSimTotals {
	var totals capturedSimTotals
	for _, res := range results {
		totals.cached += res.cachedTokens
		totals.written += res.writtenTokens
		totals.uncached += res.uncachedTokens
	}
	totals.costProxy = simCostProxy(results)
	return totals
}

// capturedSimLogRun logs one mode's per-request rows and returns its totals.
func capturedSimLogRun(t *testing.T, mode string, results []simResult) capturedSimTotals {
	t.Helper()
	for i, res := range results {
		t.Logf("CacheSim %s[%02d] cached=%d written=%d uncached=%d", mode, i, res.cachedTokens, res.writtenTokens, res.uncachedTokens)
	}
	totals := capturedSimSum(results)
	t.Logf("CacheSim %s totals: requests=%d cost_proxy=%.2f cached=%d written=%d uncached=%d", mode, len(results), totals.costProxy, totals.cached, totals.written, totals.uncached)
	return totals
}

func TestCacheSimCapturedSegmentedFixture(t *testing.T) {
	offRequests, offBoundaries := capturedSimLoadRequests(t, "captured_off.json")
	onRequests, onBoundaries := capturedSimLoadRequests(t, "captured_on.json")
	if offBoundaries != 0 {
		t.Fatalf("plain captured run records %d cache boundaries, want none", offBoundaries)
	}
	if onBoundaries == 0 {
		t.Fatal("segmented captured run records no cache boundaries, want at least one")
	}

	offTotals := capturedSimLogRun(t, "off", simulateAnthropicCache(t, offRequests))
	onTotals := capturedSimLogRun(t, "on", simulateAnthropicCache(t, onRequests))
	noBoundaryTotals := capturedSimLogRun(t, "on-without-boundaries", simulateAnthropicCache(t, capturedSimClearBoundaries(onRequests)))

	gap := onTotals.costProxy - offTotals.costProxy
	gapPercent := 100 * gap / offTotals.costProxy
	t.Logf("CacheSim gap on minus off: %.2f cost proxy (%.2f%% of the plain run), tolerance %d%%", gap, gapPercent, capturedSimCostTolerancePercent)

	if onTotals.costProxy > offTotals.costProxy*(1+capturedSimCostTolerancePercent/100.0) {
		t.Errorf("segmented run cost proxy %.2f exceeds plain run %.2f by %.2f (%.2f%%), outside the %d%% band", onTotals.costProxy, offTotals.costProxy, gap, gapPercent, capturedSimCostTolerancePercent)
	}
	if onTotals.costProxy >= noBoundaryTotals.costProxy {
		t.Errorf("segment boundary did not lower cost: with boundaries %.2f, without boundaries %.2f", onTotals.costProxy, noBoundaryTotals.costProxy)
	}
	if onTotals.cached <= offTotals.cached {
		t.Errorf("segmented run cached %d tokens, want more than the plain run's %d", onTotals.cached, offTotals.cached)
	}
}
