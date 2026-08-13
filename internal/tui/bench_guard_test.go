//go:build perfguard

// Package-level note: this file is behind the `perfguard` build tag so the
// benchmarks it runs stay out of `go test ./...`. It costs ~7s, which is
// several times the rest of the package. Run it with `make test-perf`.

package tui

import "testing"

// TestBenchmarkAllocationCeilings pins per-frame allocation ceilings for the
// TUI frame benchmarks so a regression that re-introduces per-frame
// allocations fails the test suite. Ceilings are set from baselines measured
// on this machine (go1.26.5 linux/amd64, 220x60): bytes = ceil(measured*1.15),
// allocs = ceil(measured*1.2).
func TestBenchmarkAllocationCeilings(t *testing.T) {
	// The perfguard tag keeps this out of the normal suite; this skip is the
	// separate foot-gun guard for `go test -tags perfguard -race`, where three
	// testing.Benchmark runs under race instrumentation take minutes.
	if raceEnabled {
		t.Skip("three testing.Benchmark runs under race take minutes")
	}

	cases := []struct {
		name      string
		fn        func(*testing.B)
		maxBytes  int64
		maxAllocs int64
		baseline  string
	}{
		{
			name:      "StationaryFrameHeavy",
			fn:        BenchmarkStationaryFrameHeavy,
			maxBytes:  144961,
			maxAllocs: 10,
			baseline:  "measured 126053 B/op, 8 allocs/op (reference ~126250/11)",
		},
		{
			name:      "ScrollDownHeavy",
			fn:        BenchmarkScrollDownHeavy,
			maxBytes:  518047,
			maxAllocs: 59,
			baseline:  "measured 450475 B/op, 49 allocs/op (reference ~454673/52)",
		},
		{
			name:      "ScrollDownHeavy16x",
			fn:        BenchmarkScrollDownHeavy16x,
			maxBytes:  517851,
			maxAllocs: 57,
			baseline:  "measured 450305 B/op, 47 allocs/op (reference ~450435/50)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := testing.Benchmark(tc.fn)
			// Always report, so a passing run shows the measured values
			// rather than looking like it asserted nothing.
			t.Logf("%s: %d B/op (ceiling %d), %d allocs/op (ceiling %d)",
				tc.name, res.AllocedBytesPerOp(), tc.maxBytes, res.AllocsPerOp(), tc.maxAllocs)
			if res.AllocsPerOp() == 0 || res.AllocedBytesPerOp() == 0 {
				t.Fatal("benchmark measured no allocations; metrics are not being captured")
			}
			if res.AllocsPerOp() > tc.maxAllocs {
				t.Errorf("allocs/op = %d, ceiling %d; %s", res.AllocsPerOp(), tc.maxAllocs, tc.baseline)
			}
			if res.AllocedBytesPerOp() > tc.maxBytes {
				t.Errorf("B/op = %d, ceiling %d; %s", res.AllocedBytesPerOp(), tc.maxBytes, tc.baseline)
			}
		})
	}
}
