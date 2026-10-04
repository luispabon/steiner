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
		t.Skip("eleven testing.Benchmark runs under race take minutes")
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
			maxBytes:   182768,
			maxAllocs:  11,
			baseline:   "measured 158928 B/op, 9 allocs/op (Overlay/mcp/stationary; compose and overlay render both memoised, post-WI-5)",
		},
		{
			name:       "OverlayContextStationary",
			fn:         BenchmarkOverlayContextStationary,
			checkBytes: true,
			maxBytes:   182804,
			maxAllocs:  14,
			baseline:   "measured 158960 B/op, 11 allocs/op (Overlay/context/stationary, post-WI-5)",
		},
		{
			name:       "OverlayHelpStationary",
			fn:         BenchmarkOverlayHelpStationary,
			checkBytes: true,
			maxBytes:   258134,
			maxAllocs:  11,
			baseline:   "measured 224464 B/op, 9 allocs/op (Overlay/help/stationary, post-WI-5)",
		},
		{
			name:       "OverlaySlashStationary",
			fn:         BenchmarkOverlaySlashStationary,
			checkBytes: true,
			maxBytes:   183605,
			maxAllocs:  32,
			baseline:   "measured 159656 B/op, 26 allocs/op (Overlay/slash/stationary, post-WI-6)",
		},
		{
			name:       "OverlayModelPickerStationary",
			fn:         BenchmarkOverlayModelPickerStationary,
			checkBytes: true,
			maxBytes:   184148,
			maxAllocs:  51,
			baseline:   "measured 160128 B/op, 42 allocs/op (Overlay/modelpicker/stationary, post-WI-6)",
		},
		{
			name:      "OverlayFilePickerType",
			fn:        BenchmarkOverlayFilePickerType,
			maxAllocs: 5824,
			baseline:  "measured 4853 allocs/op (Overlay/filepicker/type, post-WI-6; allocs only, bytes vary with the typed-key cycle)",
		},
		{
			name:       "DragSelectionFramePlain",
			fn:         func(b *testing.B) { benchDragFrame(b, false) },
			checkBytes: true,
			maxBytes:   302617,
			maxAllocs:  86,
			baseline:   "measured 263145 B/op, 71 allocs/op (DragSelectionFrame/plain, post-WI-7)",
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
			maxBytes:   663529,
			maxAllocs:  16959,
			baseline:   "measured 576981 B/op, 14132 allocs/op (max of 4 runs, post-WI-8e)",
		},
		{
			name:       "ContentThinkingDelta60",
			fn:         func(b *testing.B) { benchThinkingDelta(b, 60) },
			checkBytes: true,
			maxBytes:   59274,
			maxAllocs:  9,
			baseline:   "measured 51541 B/op, 7 allocs/op (post-WI-8d)",
		},
		{
			name:       "ContentIdleFrameInflight60",
			fn:         func(b *testing.B) { benchIdleFrameInflight(b, 60) },
			checkBytes: true,
			maxBytes:   53971,
			maxAllocs:  1642,
			baseline:   "measured 46931 B/op, 1368 allocs/op (max of 4 runs, post-WI-8e)",
		},
		{
			name:       "ContentToggleBlockTail60",
			fn:         func(b *testing.B) { benchToggleBlock(b, 60, 0) },
			checkBytes: true,
			maxBytes:   15142715,
			maxAllocs:  1415,
			baseline:   "measured 13167578 B/op, 1179 allocs/op (max of 4 runs, post-WI-1)",
		},
		{
			// B/op is diagnostic only: each toggle joins and reformats the whole
			// transcript, so bytes track the fixture, not per-toggle overhead. A
			// toggle that re-renders segments shows up as ~10^5 more allocs.
			name:       "SidebarToggleSteady60",
			fn:         func(b *testing.B) { benchSidebarToggleSteady(b, 60) },
			checkBytes: false,
			maxAllocs:  1364,
			baseline:   "measured 1136 allocs/op (max of 4 runs, post-WI-2; B/op intentionally unguarded)",
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
