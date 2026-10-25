//go:build perfguard && !race

package provider

import "testing"

// TestClaudeRequestEstimateResourceGuard measures the candidate ctok counter on
// whole requests at 256KiB and 1MiB. It fails when the 1MiB run misses its size,
// time, or allocation budget, or when 1MiB does not scale linearly from 256KiB.
// The !race tag excludes it under race instrumentation, which distorts timings.
func TestClaudeRequestEstimateResourceGuard(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	counter, err := claudeFixtureCounter(set.Manifest.Model)
	if err != nil {
		t.Fatal(err)
	}
	small := claudeBenchMeasure(t, set, counter, claudeBenchSmallTarget)
	large := claudeBenchMeasure(t, set, counter, claudeBenchLargeTarget)
	if err := claudeBenchCheckBudget(claudeBenchLargeTarget, large); err != nil {
		t.Error(err)
	}
	if err := claudeBenchCheckScaling(small, large); err != nil {
		t.Error(err)
	}
}

// claudeBenchMeasure runs testing.Benchmark on counter for the request sized to
// target and logs the measured bytes, time, and allocations.
func claudeBenchMeasure(t *testing.T, set claudeFixtureSet, counter textCounter, target int64) claudeBenchMeasurement {
	t.Helper()
	request, semanticBytes, err := claudeBenchSizedRequest(set, target)
	if err != nil {
		t.Fatal(err)
	}
	res := testing.Benchmark(func(b *testing.B) {
		claudeBenchEstimate(b, counter, request, semanticBytes)
	})
	m := claudeBenchMeasurement{
		Bytes:       res.Bytes,
		NsPerOp:     res.NsPerOp(),
		BytesPerOp:  res.AllocedBytesPerOp(),
		AllocsPerOp: res.AllocsPerOp(),
	}
	t.Logf("candidate target=%d: semantic bytes=%d, %d ns/op, %d B/op, %d allocs/op",
		target, m.Bytes, m.NsPerOp, m.BytesPerOp, m.AllocsPerOp)
	if m.Bytes != semanticBytes {
		t.Errorf("SetBytes = %d, want semantic bytes %d", m.Bytes, semanticBytes)
	}
	return m
}
