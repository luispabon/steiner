//go:build perfguard && !race

package provider

import "testing"

// TestClaudeRequestEstimateResourceGuard measures the candidate ctok counter on
// synthetic requests at 256KiB and 1MiB. It fails when the 1MiB run misses its
// size, time, or allocation budget, or when 1MiB does not scale linearly from
// 256KiB. The !race tag excludes it under race instrumentation, which distorts
// timings.
func TestClaudeRequestEstimateResourceGuard(t *testing.T) {
	payloads := mustClaudeSyntheticPayloads(t)
	counter, err := claudeSyntheticCounter()
	if err != nil {
		t.Fatal(err)
	}
	small := claudeBenchMeasureTarget(t, payloads, counter, claudeBenchSmallTarget)
	large := claudeBenchMeasureTarget(t, payloads, counter, claudeBenchLargeTarget)
	if err := claudeBenchCheckBudget(claudeBenchLargeTarget, large); err != nil {
		t.Error(err)
	}
	if err := claudeBenchCheckScaling(small, large); err != nil {
		t.Error(err)
	}
}

// claudeBenchMeasureTarget measures counter on the request sized to target and
// logs the result. A failed measurement stops the test with the counter's error.
func claudeBenchMeasureTarget(t *testing.T, payloads map[string]string, counter textCounter, target int64) claudeBenchMeasurement {
	t.Helper()
	request, semanticBytes, err := claudeBenchSizedRequest(payloads, target)
	if err != nil {
		t.Fatal(err)
	}
	m, err := claudeBenchMeasure(request, semanticBytes, counter)
	if err != nil {
		t.Fatalf("measure candidate target=%d: %v", target, err)
	}
	t.Logf("candidate target=%d: semantic bytes=%d, %d ns/op, %d B/op, %d allocs/op",
		target, m.Bytes, m.NsPerOp, m.BytesPerOp, m.AllocsPerOp)
	return m
}
