package tui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// countingModel wraps the real Model so the harness can count View calls and
// accumulate the time the event loop spends inside View, without touching
// production code.
type countingModel struct {
	m         *Model
	views     atomic.Int64
	updates   atomic.Int64
	viewNanos atomic.Int64
	updNanos  atomic.Int64

	// Guard state, written only on the event-loop goroutine (inside View).
	prevContent       string
	changedFrames     int64
	ySeen             bool
	yOffMin           int
	yOffMax           int
	yOffsets          map[int]struct{}
	injectedViewDelay time.Duration
}

func (c *countingModel) Init() tea.Cmd { return c.m.Init() }

func (c *countingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	t0 := time.Now()
	_, cmd := c.m.Update(msg)
	c.updNanos.Add(int64(time.Since(t0)))
	c.updates.Add(1)
	return c, cmd
}

func (c *countingModel) View() tea.View {
	t0 := time.Now()
	if c.injectedViewDelay > 0 {
		time.Sleep(c.injectedViewDelay)
	}
	v := c.m.View()
	c.viewNanos.Add(int64(time.Since(t0)))
	c.views.Add(1)
	c.trackView(v)
	return v
}

// trackView records guard state from inside View. It runs on the event-loop
// goroutine only, so its fields need no locking.
func (c *countingModel) trackView(v tea.View) {
	y := c.m.viewport.YOffset()
	if !c.ySeen {
		c.ySeen = true
		c.yOffMin = y
		c.yOffMax = y
	} else {
		c.yOffMin = min(c.yOffMin, y)
		c.yOffMax = max(c.yOffMax, y)
	}
	if len(c.yOffsets) < 64 {
		c.yOffsets[y] = struct{}{}
	}
	if v.Content != c.prevContent {
		c.changedFrames++
		c.prevContent = v.Content
	}
}

// countingWriter is the program output sink. Write is called from the
// renderer tick goroutine, so it must be mutex-protected.
type countingWriter struct {
	mu     sync.Mutex
	writes int64
	bytes  int64
	times  []time.Time
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	w.bytes += int64(len(p))
	w.times = append(w.times, time.Now())
	return len(p), nil
}

func (w *countingWriter) snapshot() (writes, bytes int64, times []time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes, w.bytes, append([]time.Time(nil), w.times...)
}

func heavyModel() *Model {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)
	m.syncViewport()
	m.scrollUp(30) // leave the bottom so both scroll directions can move
	return m
}

// headlessResult carries everything a headless run measured: raw counters,
// guard state read after the event loop finished, and derived rates.
type headlessResult struct {
	elapsed    time.Duration
	sends      int64
	updates    int64
	views      int64
	writes     int64
	bytes      int64
	viewNanos  int64
	updNanos   int64
	cpuNanos   int64
	writeTimes []time.Time

	width           int
	height          int
	yOffMin         int
	yOffMax         int
	distinctOffsets int
	changedFrames   int64

	sendsPerSec   float64
	viewsPerSec   float64
	writesPerSec  float64
	avgViewNanos  float64
	viewWallShare float64
	cpuWallShare  float64
}

// String renders a compact summary for -v test output.
func (r headlessResult) String() string {
	return fmt.Sprintf("elapsed=%s sends=%d updates=%d views=%d writes=%d bytes=%d "+
		"yOff=[%d,%d] distinctOffsets=%d changedFrames=%d "+
		"sends/s=%.0f views/s=%.0f writes/s=%.0f avgView=%s viewWallShare=%.2f cpuWallShare=%.2f",
		r.elapsed, r.sends, r.updates, r.views, r.writes, r.bytes,
		r.yOffMin, r.yOffMax, r.distinctOffsets, r.changedFrames,
		r.sendsPerSec, r.viewsPerSec, r.writesPerSec,
		time.Duration(r.avgViewNanos), r.viewWallShare, r.cpuWallShare)
}

// runConfig carries the expected renderer FPS, the Guard 4 threshold
// reference.
type runConfig struct {
	fps int
}

func runHeadless(tb testing.TB, d time.Duration, interval time.Duration, fps int) headlessResult {
	return runHarness(tb, d, interval, fps, 0, true)
}

func runIdle(tb testing.TB, d time.Duration, fps int) headlessResult {
	return runHarness(tb, d, 0, fps, 0, false)
}

// runHeadlessWithInjectedViewDelay runs the scrolling harness with a fixed
// per-View sleep injected through countingModel. Used only by the self-test to
// prove the harness measures real View time.
func runHeadlessWithInjectedViewDelay(tb testing.TB, d, interval time.Duration, fps int, delay time.Duration) headlessResult {
	return runHarness(tb, d, interval, fps, delay, true)
}

func runHarness(tb testing.TB, d, interval time.Duration, fps int, injectedViewDelay time.Duration, scroll bool) headlessResult {
	tb.Helper()

	m := heavyModel()
	c := &countingModel{m: m, yOffsets: make(map[int]struct{}), injectedViewDelay: injectedViewDelay}
	w := &countingWriter{}

	// Every option is load-bearing and the order matters: an empty input and
	// a non-TTY output keep the harness off the real terminal, WithoutSignals
	// and WithoutCatchPanics keep it hermetic, the environment pins a known
	// TERM, and WithWindowSize is what stops the model being resized to 0x0.
	p := tea.NewProgram(c,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(w),
		tea.WithoutSignals(),
		tea.WithoutCatchPanics(),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithWindowSize(220, 60), // WITHOUT THIS THE MODEL IS RESIZED TO 0x0
		tea.WithFPS(fps),
	)

	start := time.Now()
	ruBefore := processCPUNanos()

	var sends atomic.Int64
	go func() {
		if scroll {
			// Long-period sweep (direction flips every 40 sends), NOT a
			// 2-position oscillation, so the viewport traverses a wide band
			// of offsets.
			for n := 0; time.Since(start) < d; n++ {
				if (n/40)%2 == 0 {
					p.Send(mouseWheelMsg{direction: "up"})
				} else {
					p.Send(mouseWheelMsg{direction: "down"})
				}
				sends.Add(1)
				if interval > 0 {
					time.Sleep(interval)
				}
			}
		} else {
			time.Sleep(d)
		}
		p.Quit()
	}()

	_, err := p.Run()
	if err != nil {
		tb.Fatalf("tea program run: %v", err)
	}

	elapsed := time.Since(start)
	cpuNanos := processCPUNanos() - ruBefore
	writes, bytes, writeTimes := w.snapshot()

	r := headlessResult{
		elapsed:    elapsed,
		sends:      sends.Load(),
		updates:    c.updates.Load(),
		views:      c.views.Load(),
		writes:     writes,
		bytes:      bytes,
		viewNanos:  c.viewNanos.Load(),
		updNanos:   c.updNanos.Load(),
		cpuNanos:   cpuNanos,
		writeTimes: writeTimes,

		width:           c.m.width,
		height:          c.m.height,
		yOffMin:         c.yOffMin,
		yOffMax:         c.yOffMax,
		distinctOffsets: len(c.yOffsets),
		changedFrames:   c.changedFrames,

		sendsPerSec:  float64(sends.Load()) / elapsed.Seconds(),
		viewsPerSec:  float64(c.views.Load()) / elapsed.Seconds(),
		writesPerSec: float64(writes) / elapsed.Seconds(),
	}
	if r.views > 0 {
		r.avgViewNanos = float64(r.viewNanos) / float64(r.views)
	}
	r.viewWallShare = float64(r.viewNanos) / float64(elapsed.Nanoseconds())
	r.cpuWallShare = float64(cpuNanos) / float64(elapsed.Nanoseconds())

	assertHarnessLive(tb, r, runConfig{fps: fps})
	return r
}

// assertHarnessLive checks that a headless run exercised the real event loop,
// the real renderer, and a moving viewport. All guards use Errorf so one
// failure does not hide the others, and every message names the observed value
// and the historical fault it protects against.
func assertHarnessLive(tb testing.TB, r headlessResult, cfg runConfig) {
	tb.Helper()

	// Guard 1 (geometry, deterministic): the model must end at the window
	// size WithWindowSize requested. A missing option leaves the model at
	// 0x0 and every render degenerates.
	if r.width != 220 || r.height != 60 {
		tb.Errorf("guard 1: model geometry = %dx%d, want 220x60; protects against the 0x0 WindowSizeMsg resize fault", r.width, r.height)
	}

	if r.sends > 0 {
		// Guard 2 (viewport movement, deterministic): a pinned offset means
		// the sweep never reached the viewport — the stationary-frame
		// benchmark fault where five tranches measured a never-scrolling
		// frame.
		if r.yOffMax <= r.yOffMin {
			tb.Errorf("guard 2: viewport never moved: yOffset pinned at %d (min=%d max=%d); protects against the stationary-frame benchmark fault", r.yOffMin, r.yOffMin, r.yOffMax)
		}

		// Guard 3 (frame churn, deterministic): at least a quarter of View
		// calls must produce a different frame; protects against a View that
		// has stopped varying.
		if r.changedFrames*4 < r.views {
			tb.Errorf("guard 3: changedFrames=%d views=%d (want changedFrames*4 >= views); protects against a View that has stopped varying", r.changedFrames, r.views)
		}

		// Guard 3b (offset diversity, deterministic): a 2-position sweep
		// yields exactly 2 distinct offsets and passes Guard 3, so require a
		// wide band; protects against the 2-position oscillation artifact.
		if r.distinctOffsets < 8 {
			tb.Errorf("guard 3b: distinctOffsets=%d, want >= 8; protects against the 2-position oscillation artifact", r.distinctOffsets)
		}
	}

	// Guard 4 (LOOSE timing-based liveness): when messages are delivered at
	// or above the renderer FPS, the renderer must actually write frames at a
	// comparable rate. This is headroom for slow/loaded CI, not a performance
	// assertion; it only catches viewEquals collapsing every terminal write.
	//
	// This comparison is skipped under -race: race instrumentation slows the
	// 220x60 frame flush past the 60Hz tick period, so the healthy workload
	// itself writes at ~17-32/s and can cross the 30/s threshold (measured
	// 16.6 and 21.8 writes/s in two of 20 race runs). The harness still runs
	// under -race for data-race coverage on the tick goroutine and shared
	// writer; only this one wall-clock threshold is skipped there.
	if !raceEnabled && r.sendsPerSec >= float64(cfg.fps) {
		threshold := 0.5 * float64(cfg.fps)
		if r.writesPerSec < threshold {
			tb.Errorf("guard 4: writes/s=%.1f below threshold %.1f at fps=%d; protects against viewEquals collapsing terminal writes", r.writesPerSec, threshold, cfg.fps)
		}
	}

	// Guard 5 (event loop ran): View runs after every Update plus init and
	// resize frames, so views must at least match sends, and View must have
	// consumed real time; protects against the event-loop-not-running fault.
	if r.views < r.sends || r.viewNanos <= 0 {
		tb.Errorf("guard 5: views=%d sends=%d viewNanos=%d; protects against the event-loop-not-running fault", r.views, r.sends, r.viewNanos)
	}
}

// harnessFPS pins the renderer rate for the committed harness runs at the
// production default of 60. On a normal (non-race) build Guard 4's 0.5*fps
// threshold cleanly separates the healthy ~60 writes/s from the
// viewEquals-collapsed ~19/s oscillation artifact. Guard 4's timing threshold
// is skipped under -race, where it is not meaningful; see assertHarnessLive.
const harnessFPS = 60

func TestHarnessScrolling(t *testing.T) {
	r := runHeadless(t, 800*time.Millisecond, 1*time.Millisecond, harnessFPS)
	t.Logf("scrolling harness: %s", r)
}

func TestHarnessIdle(t *testing.T) {
	r := runIdle(t, 400*time.Millisecond, harnessFPS)
	t.Logf("idle harness: %s", r)
}

func TestHarnessSelfTest(t *testing.T) {
	base := runHeadlessWithInjectedViewDelay(t, 600*time.Millisecond, 2*time.Millisecond, harnessFPS, 0)
	delayed := runHeadlessWithInjectedViewDelay(t, 600*time.Millisecond, 2*time.Millisecond, harnessFPS, 5*time.Millisecond)
	t.Logf("self-test base avgView=%s delayed avgView=%s", time.Duration(base.avgViewNanos), time.Duration(delayed.avgViewNanos))
	if delta := delayed.avgViewNanos - base.avgViewNanos; delta < 4*float64(time.Millisecond) {
		t.Fatalf("self-test: injected 5ms View delay not measured: base avgView=%s, delayed avgView=%s, delta=%s; protects against a harness that does not measure real time", time.Duration(base.avgViewNanos), time.Duration(delayed.avgViewNanos), time.Duration(delta))
	}
}
