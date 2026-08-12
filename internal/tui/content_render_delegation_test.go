package tui

import "testing"

func TestDelegationCompleteMetaIncludesCacheHitRateWhenOK(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		resultStatus:  "complete",
		turnCount:     4,
		toolCallCount: 12,
		tokenCount:    8123,
		elapsed:       "12.4s",
		cacheHitRate:  0.952,
		cacheHitOK:    true,
	}

	meta := delegationCompleteMeta(dd)

	if len(meta) == 0 || meta[len(meta)-1] != "cache 95.2%" {
		t.Errorf("delegationCompleteMeta() = %v, want last entry %q", meta, "cache 95.2%")
	}
}

func TestDelegationCompleteMetaOmitsCacheHitRateWhenNotOK(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		resultStatus:  "complete",
		turnCount:     4,
		toolCallCount: 12,
		tokenCount:    8123,
		elapsed:       "12.4s",
		cacheHitOK:    false,
	}

	meta := delegationCompleteMeta(dd)

	for _, part := range meta {
		if part == "cache 95.2%" || part == "cache 0.0%" {
			t.Errorf("delegationCompleteMeta() = %v, want no cache entry when cacheHitOK is false", meta)
		}
	}
}
