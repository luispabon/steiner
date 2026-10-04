//go:build perfguard

// Package-level note: this file is behind the `perfguard` build tag so the
// benchmarks it runs stay out of `go test ./...`. It costs ~15s, which is
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
	// separate foot-gun guard for `go test -tags perfguard -race`, where eight
	// testing.Benchmark runs under race instrumentation take minutes.
	if raceEnabled {
		t.Skip("eight testing.Benchmark runs under race take minutes")
	}

	cases := []struct {
		name       string
		fn         func(*testing.B)
		setup      func() error
		checkBytes bool
		maxBytes   int64
		maxAllocs  int64
		baseline   string
	}{
		{
			name:       "StationaryFrameFixedViewport",
			setup:      func() error { _, err := setupFixedViewportBenchmark(); return err },
			fn:         BenchmarkStationaryFrameFixedViewport,
			checkBytes: true,
			maxBytes:   57813,
			maxAllocs:  10,
			baseline:   "measured 50272 B/op, 8 allocs/op (reference ~57813/10)",
		},
		{
			// B/op is diagnostic only here: bottom-pinned visible payload changes with layout.
			name:       "StationaryFrameBottomPinnedHeavy",
			fn:         BenchmarkStationaryFrameBottomPinnedHeavy,
			checkBytes: false,
			maxAllocs:  10,
			baseline:   "measured 8 allocs/op (B/op intentionally unguarded)",
		},
		{
			name:       "ScrollDownHeavy",
			fn:         BenchmarkScrollDownHeavy,
			checkBytes: true,
			maxBytes:   518047,
			maxAllocs:  59,
			baseline:   "measured 450475 B/op, 49 allocs/op (reference ~454673/52)",
		},
		{
			name:       "ScrollDownHeavy16x",
			fn:         BenchmarkScrollDownHeavy16x,
			checkBytes: true,
			maxBytes:   517851,
			maxAllocs:  57,
			baseline:   "measured 450305 B/op, 47 allocs/op (reference ~450435/50)",
		},
		{
			name:       "OverlayMCPStationary",
			fn:         BenchmarkOverlayMCPStationary,
			checkBytes: true,
			maxBytes:   410688,
			maxAllocs:  4124,
			baseline:   "measured 357119 B/op, 3436 allocs/op (Overlay/mcp/stationary; compose memoised, overlay render dominates)",
		},
		{
			name:       "AuditTickUpdateStreaming",
			fn:         BenchmarkAuditTickUpdateStreaming,
			checkBytes: true,
			maxBytes:   6533808,
			maxAllocs:  2918,
			baseline:   "measured 5681572 B/op, 2431 allocs/op (max of 4 runs, post-WI-1)",
		},
		{
			// Content guards use a 60-message transcript to keep the run short.
			name:       "ContentToolCallFinish60",
			fn:         func(b *testing.B) { benchToolCallFinish(b, 60) },
			checkBytes: true,
			maxBytes:   15932102,
			maxAllocs:  17237,
			baseline:   "measured 13854001 B/op, 14364 allocs/op (max of 4 runs, post-WI-1)",
		},
		{
			name:       "ContentIdleFrameInflight60",
			fn:         func(b *testing.B) { benchIdleFrameInflight(b, 60) },
			checkBytes: true,
			maxBytes:   13157598,
			maxAllocs:  1678,
			baseline:   "measured 11441389 B/op, 1398 allocs/op (max of 4 runs, post-WI-1)",
		},
		{
			name:       "ContentToggleBlockTail60",
			fn:         func(b *testing.B) { benchToggleBlock(b, 60, 0) },
			checkBytes: true,
			maxBytes:   15142715,
			maxAllocs:  1415,
			baseline:   "measured 13167578 B/op, 1179 allocs/op (max of 4 runs, post-WI-1)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				if err := tc.setup(); err != nil {
					t.Fatal(err)
				}
			}
			res := testing.Benchmark(tc.fn)
			// Always report, so a passing run shows the measured values
			// rather than looking like it asserted nothing.
			if tc.checkBytes {
				t.Logf("%s: %d B/op (ceiling %d), %d allocs/op (ceiling %d)",
					tc.name, res.AllocedBytesPerOp(), tc.maxBytes, res.AllocsPerOp(), tc.maxAllocs)
			} else {
				t.Logf("%s: %d B/op (diagnostic only), %d allocs/op (ceiling %d)",
					tc.name, res.AllocedBytesPerOp(), res.AllocsPerOp(), tc.maxAllocs)
			}
			if res.AllocsPerOp() == 0 || res.AllocedBytesPerOp() == 0 {
				t.Fatal("benchmark measured no allocations; metrics are not being captured")
			}
			if res.AllocsPerOp() > tc.maxAllocs {
				t.Errorf("allocs/op = %d, ceiling %d; %s", res.AllocsPerOp(), tc.maxAllocs, tc.baseline)
			}
			if tc.checkBytes && res.AllocedBytesPerOp() > tc.maxBytes {
				t.Errorf("B/op = %d, ceiling %d; %s", res.AllocedBytesPerOp(), tc.maxBytes, tc.baseline)
			}
		})
	}
}
