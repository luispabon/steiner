//go:build perfguard

// Package-level note: this file is behind the `perfguard` build tag so the
// benchmarks it runs stay out of `go test ./...`. It costs ~15s, which is
// several times the rest of the package. Run it with `make test-perf`.

package tui

import (
	"flag"
	"testing"
)

// contentIterations is the fixed b.N for rows whose cheap timed op sits next
// to expensive untimed fixture rebuilds. With the default 1s target,
// testing.Benchmark raises b.N until the timed work fills a second and those
// rows spend minutes in setup. Ceilings are per-op averages, so a fixed count
// measures the same thing. Other rows keep the default target: their one-off
// warm-up allocations need many ops to amortise.
const contentIterations = "300x"

// TestBenchmarkAllocationCeilings pins per-frame allocation ceilings for the
// TUI frame benchmarks so a regression that re-introduces per-frame
// allocations fails the test suite. Ceilings are set from baselines measured
// on this machine (go1.26.5 linux/amd64, 220x60): bytes = ceil(measured*1.15),
// allocs = ceil(measured*1.2).
func TestBenchmarkAllocationCeilings(t *testing.T) {
	// The perfguard tag keeps this out of the normal suite; this skip is the
	// separate foot-gun guard for `go test -tags perfguard -race`, where the
	// testing.Benchmark runs under race instrumentation take minutes.
	if raceEnabled {
		t.Skip("the testing.Benchmark runs under race take minutes")
	}

	cases := []struct {
		name       string
		fn         func(*testing.B)
		setup      func() error
		benchtime  string // fixed -test.benchtime for this row; empty keeps the default
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
			maxBytes:   314659,
			maxAllocs:  11,
			baseline:   "branch 273616 B/op, 9 allocs/op vs origin/main 240848 B/op, 9 allocs/op on the same fixture; equal-visible-content branch=main 224464 B/op, 9 allocs/op; top-pinned both 486609 B/op. Delta is four heavier visible thinking rows from a 4-line-shorter transcript, not render work",
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
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchToolCallFinish(b, 60) },
			checkBytes: true,
			maxBytes:   663529,
			maxAllocs:  16959,
			baseline:   "measured 576981 B/op, 14132 allocs/op (max of 4 runs, post-WI-8e)",
		},
		{
			name:       "ContentThinkingDelta60",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchThinkingDelta(b, 60) },
			checkBytes: true,
			maxBytes:   59274,
			maxAllocs:  9,
			baseline:   "measured 51541 B/op, 7 allocs/op (post-WI-8d)",
		},
		{
			// The delta appends to an 8KB (resp. ~32KB) streaming buffer; a preview
			// that re-wraps the whole buffer shows up as ~10^4 (~3.6*10^4) allocs.
			name:       "ContentStreamDeltaLongBuffer",
			benchtime:  contentIterations,
			fn:         BenchmarkContentStreamDeltaLongBuffer,
			checkBytes: true,
			maxBytes:   128245,
			maxAllocs:  364,
			baseline:   "measured 111517 B/op, 303 allocs/op (max of 3 runs, post-WI-8f)",
		},
		{
			name:       "ContentStreamDeltaLongBuffer32K",
			benchtime:  contentIterations,
			fn:         BenchmarkContentStreamDeltaLongBuffer32K,
			checkBytes: true,
			maxBytes:   283935,
			maxAllocs:  674,
			baseline:   "measured 246899 B/op, 561 allocs/op (max of 3 runs, post-WI-8f)",
		},
		{
			name:       "ContentIdleFrameInflight60",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchIdleFrameInflight(b, 60) },
			checkBytes: true,
			maxBytes:   39207,
			maxAllocs:  416,
			baseline:   "measured 34093 B/op, 346 allocs/op (max of 4 runs, 300x); origin/main 47251 B/op, 1368 allocs/op",
		},
		{
			// Group of 1 active + 2 settled expanded children, idle frame.
			name:       "GroupMixedIdleExpanded2",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchIdleFrameMixed(b, 2, true, false) },
			checkBytes: true,
			maxBytes:   197510,
			maxAllocs:  2026,
			baseline:   "measured 171748 B/op, 1688 allocs/op (max of 4 runs, 300x); origin/main 776227 B/op, 17320 allocs/op for the same events",
		},
		{
			// Group of 1 active + 5 settled collapsed children, idle frame.
			name:       "GroupMixedIdleCollapsed5",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchIdleFrameMixed(b, 5, false, false) },
			checkBytes: true,
			maxBytes:   49595,
			maxAllocs:  378,
			baseline:   "measured 43126 B/op, 315 allocs/op (max of 4 runs, 300x); origin/main 129213 B/op, 3399 allocs/op for the same events",
		},
		{
			// Same group as GroupMixedIdleExpanded2, with spinner ticks before each frame.
			name:       "GroupMixedSpinnerExpanded2",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchIdleFrameMixed(b, 2, true, true) },
			checkBytes: true,
			maxBytes:   217056,
			maxAllocs:  2225,
			baseline:   "measured 188744 B/op, 1854 allocs/op (max of 4 runs, 300x); origin/main 775081 B/op, 17320 allocs/op for the same events",
		},
		{
			// Cold render: every group cache is dropped and a settled card is dirtied per op.
			name:       "GroupMixedFirstExpanded2",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchFirstRenderMixed(b, 2, true) },
			checkBytes: true,
			maxBytes:   1181359,
			maxAllocs:  24045,
			baseline:   "ceiling is origin/main parity +15% bytes and +20% allocs (1027268*1.15 = 1181359, 20037*1.2 = 24045); branch measured max of 4 runs 1118806 B/op, 20761 allocs/op (300x), 1.09x main bytes and 1.04x main allocs",
		},
		{
			name:       "ContentToggleBlockTail60",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchToggleBlock(b, 60, 0) },
			checkBytes: true,
			maxBytes:   1882715,
			maxAllocs:  1383,
			baseline:   "measured 1637143 B/op, 1152 allocs/op (max of 4 runs, post-WI-10)",
		},
		{
			name:       "ContentToggleBlockEarly60",
			benchtime:  contentIterations,
			fn:         func(b *testing.B) { benchToggleBlock(b, 60, 40) },
			checkBytes: true,
			maxBytes:   692823,
			maxAllocs:  1424,
			baseline:   "measured 602454 B/op, 1186 allocs/op (max of 4 runs, post-WI-10)",
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
		{
			name:       "DevLogRealisticStationary",
			fn:         benchDevLogRealisticStationary,
			checkBytes: true,
			maxBytes:   182804,
			maxAllocs:  14,
			baseline:   "measured 158960 B/op, 11 allocs/op (max of 4 runs; DevLog/realistic/stationary, post-F6; pre-F6 was ~3.6 MiB / 145k allocs)",
		},
		{
			name:       "DevLogRealisticClick",
			fn:         benchDevLogRealisticClick,
			checkBytes: true,
			maxBytes:   308542,
			maxAllocs:  46,
			baseline:   "measured 268297 B/op, 38 allocs/op (max of 4 runs; DevLog/realistic/click, post-F6; pre-F6 was ~3.7 MiB / 145k allocs)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				if err := tc.setup(); err != nil {
					t.Fatal(err)
				}
			}
			if tc.benchtime != "" {
				setBenchtime(t, tc.benchtime)
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

// benchDevLogRealisticStationary and benchDevLogRealisticClick adapt the dev log
// helpers to the func(*testing.B) shape of the cases table. They live in this
// tagged file so the default build has no unused functions.
func benchDevLogRealisticStationary(b *testing.B) {
	benchDevLogStationary(b, devLogBenchParams(false))
}

func benchDevLogRealisticClick(b *testing.B) {
	benchDevLogClick(b, devLogBenchParams(false))
}

// setBenchtime sets -test.benchtime for the rest of the calling (sub)test and
// restores the previous value when it ends.
func setBenchtime(t *testing.T, value string) {
	t.Helper()
	prev := flag.Lookup("test.benchtime").Value.String()
	if err := flag.Set("test.benchtime", value); err != nil {
		t.Fatalf("set benchtime: %v", err)
	}
	t.Cleanup(func() {
		if err := flag.Set("test.benchtime", prev); err != nil {
			t.Errorf("restore benchtime: %v", err)
		}
	})
}
