package agent

import (
	"testing"
	"time"
)

// TestConversationDriverSeedsAndSnapshotsLastMainRequest proves a host can seed
// the carried last main request from persisted state, that the seed is what the
// next run receives, and that snapshots carry it for persistence.
func TestConversationDriverSeedsAndSnapshotsLastMainRequest(t *testing.T) {
	t.Parallel()
	seed := lastMainRequestFixture(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	h := newDriverHarnessOpts(t, nil, nil, func(opts *DriverOptions) {
		opts.LastMainRequest = seed
	})
	h.start()
	if got := h.d.LastMainRequest(); got != seed {
		t.Fatalf("seeded LastMainRequest = %+v, want %+v", got, seed)
	}
	if got := h.d.Snapshot().LastMainRequest; got != seed {
		t.Fatalf("initial snapshot LastMainRequest = %+v, want %+v", got, seed)
	}

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	if first.in.LastMainRequest != seed {
		t.Fatalf("run LastMainRequest = %+v, want the seed %+v", first.in.LastMainRequest, seed)
	}
	next := LastMainRequest{At: seed.At.Add(time.Minute), ProviderAlias: "openai", BackendModelID: "gpt-next", PromptCacheKey: "cache-key"}
	finishRunWith(first, DriverRunOutput{LastMainRequest: next})
	h.waitQuiescent()
	if got := h.d.Snapshot().LastMainRequest; got != next {
		t.Fatalf("snapshot after a run = %+v, want %+v", got, next)
	}
	h.closeDriver()
}
