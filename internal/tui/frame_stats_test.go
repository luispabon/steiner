package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
)

type recordingSink struct {
	mu   sync.Mutex
	recs []diagnostics.Record
	gate chan struct{}
}

func (s *recordingSink) Write(rec diagnostics.Record) {
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
}

func (s *recordingSink) records() []diagnostics.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]diagnostics.Record(nil), s.recs...)
}

func payloadOf(t *testing.T, rec diagnostics.Record) frameStatsPayload {
	t.Helper()
	p, ok := rec.Payload.(frameStatsPayload)
	if !ok {
		t.Fatalf("payload type = %T, want frameStatsPayload", rec.Payload)
	}
	return p
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestFrameStats(sink FrameStatsSink) (*frameStats, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	f := newFrameStats(sink)
	f.now = clk.now
	return f, clk
}

func closeFrameStats(t *testing.T, f *frameStats) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.Close(ctx)
}

func TestClassifyMsg(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"key", tea.KeyPressMsg{}, "key"},
		{"wheel", mouseWheelMsg{}, "mouse_wheel"},
		{"click", mouseClickMsg{}, "mouse_click"},
		{"release", mouseReleaseMsg{}, "mouse_release"},
		{"motion", mouseMotionMsg{}, "mouse_motion"},
		{"raw wheel", tea.MouseWheelMsg{}, "mouse_raw"},
		{"raw click", tea.MouseClickMsg{}, "mouse_raw"},
		{"tick", tickMsg{}, "tick"},
		{"sync debounce", syncDebounceFiredMsg{}, "sync_debounce"},
		{"resize reflow", resizeReflowFiredMsg{}, "resize_reflow"},
		{"runtime event", runtimeEventMsg{Event: output.Event{Type: output.EventTypeAssistantChunk}}, "runtime_event:" + output.EventTypeAssistantChunk},
		{"window size", tea.WindowSizeMsg{}, "window_size"},
		{"composer blink", composerBlinkMsg{}, "composer_blink"},
		{"session tick", sessionTickMsg{}, "session_tick"},
		{"drag autoscroll", dragAutoScrollTickMsg{}, "drag_autoscroll"},
		{"paste", tea.PasteMsg{}, "paste"},
		{"fallback", struct{ x int }{}, "other:struct { x int }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyMsg(tt.msg).String(); got != tt.want {
				t.Errorf("classifyMsg() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHistBucket(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{50 * time.Microsecond, 0},
		{51 * time.Microsecond, 1},
		{time.Millisecond, 4},
		{1001 * time.Microsecond, 5},
		{512 * time.Millisecond, 13},
		{513 * time.Millisecond, 14},
		{time.Hour, 14},
	}
	for _, tt := range tests {
		if got := histBucket(tt.d.Nanoseconds()); got != tt.want {
			t.Errorf("histBucket(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

func TestFrameStatsAggregatesAndRollsOverWindow(t *testing.T) {
	sink := &recordingSink{}
	f, clk := newTestFrameStats(sink)

	snap := frameSnapshot{overlayOpen: true, subagentsActive: 2, transcriptLines: 40, width: 100, height: 30}
	clk.t = clk.t.Add(time.Second)
	f.recordUpdate(tickMsg{}, 3*time.Millisecond, snap)
	f.recordView(5 * time.Millisecond)
	clk.t = clk.t.Add(time.Second)
	snap.subagentsActive = 1
	f.recordUpdate(tickMsg{}, 100*time.Microsecond, snap)
	f.recordView(time.Millisecond)
	f.recordUpdate(tea.KeyPressMsg{}, time.Microsecond, snap)
	clk.t = clk.t.Add(frameStatsWindow)
	f.recordUpdate(tea.KeyPressMsg{}, time.Microsecond, snap)
	closeFrameStats(t, f)

	recs := sink.records()
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3 (key+tick in window 1, key in window 2)", len(recs))
	}
	key1, tick1, key2 := payloadOf(t, recs[0]), payloadOf(t, recs[1]), payloadOf(t, recs[2])
	if key1.Type != "key" || tick1.Type != "tick" || key2.Type != "key" {
		t.Fatalf("types = %s,%s,%s, want key,tick,key (sorted per window)", key1.Type, tick1.Type, key2.Type)
	}
	if tick1.UpdateCount != 2 || tick1.ViewCount != 2 {
		t.Errorf("tick counts = %d/%d, want 2/2", tick1.UpdateCount, tick1.ViewCount)
	}
	if tick1.UpdateMaxMs != 3 || tick1.ViewMaxMs != 5 {
		t.Errorf("tick max = %v/%v, want 3/5", tick1.UpdateMaxMs, tick1.ViewMaxMs)
	}
	if tick1.UpdateHist[1] != 1 || tick1.UpdateHist[6] != 1 {
		t.Errorf("tick update hist = %v, want one in bucket 1 and one in 6", tick1.UpdateHist)
	}
	if key1.ViewCount != 0 {
		t.Errorf("key view count = %d, want 0 (the view paired with the later tick update)", key1.ViewCount)
	}
	if tick1.SubagentsRunningMax != 2 || tick1.TranscriptLines != 40 || tick1.Width != 100 || tick1.Height != 30 {
		t.Errorf("window fields = %+v", tick1)
	}
	if tick1.WindowID != 1 || key2.WindowID != 2 {
		t.Errorf("window ids = %d,%d, want 1,2", tick1.WindowID, key2.WindowID)
	}
	if tick1.OverlayOpenMs < 1999 {
		t.Errorf("overlay_open_ms = %v, want about 12000 minus the first second", tick1.OverlayOpenMs)
	}
	if key2.UpdateCount != 1 {
		t.Errorf("window 2 key count = %d, want 1", key2.UpdateCount)
	}
	for _, rec := range recs {
		if rec.Kind != diagnostics.KindTUI {
			t.Errorf("kind = %q, want %q", rec.Kind, diagnostics.KindTUI)
		}
	}
}

func TestFrameStatsUnpairedView(t *testing.T) {
	sink := &recordingSink{}
	f, _ := newTestFrameStats(sink)
	f.recordView(time.Millisecond)
	closeFrameStats(t, f)
	if got := len(sink.records()); got != 0 {
		t.Fatalf("records = %d, want 0: a window never opened by an Update flushes nothing", got)
	}

	sink = &recordingSink{}
	f, _ = newTestFrameStats(sink)
	f.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
	f.recordView(time.Millisecond)
	f.recordView(time.Millisecond)
	closeFrameStats(t, f)
	recs := sink.records()
	if len(recs) != 2 || payloadOf(t, recs[1]).Type != "view_unpaired" {
		t.Fatalf("records = %+v, want tick plus view_unpaired", recs)
	}
}

func TestFrameStatsDropsWhenQueueFull(t *testing.T) {
	sink := &recordingSink{gate: make(chan struct{})}
	f, clk := newTestFrameStats(sink)
	// One window is held by the blocked writer, frameStatsQueue more fill the
	// channel, and the next two are dropped.
	windows := frameStatsQueue + 3
	for i := 0; i < windows; i++ {
		f.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
		clk.t = clk.t.Add(frameStatsWindow)
	}
	f.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
	if f.dropped < 1 {
		t.Fatalf("dropped = %d, want at least 1 with a blocked writer", f.dropped)
	}
	dropped := f.dropped
	close(sink.gate)
	deadline := time.Now().Add(2 * time.Second)
	for len(f.ch) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(f.ch) > 0 {
		t.Fatal("writer did not drain the queue after release")
	}
	clk.t = clk.t.Add(frameStatsWindow)
	f.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
	closeFrameStats(t, f)
	recs := sink.records()
	if len(recs) == 0 {
		t.Fatal("no records written after the writer was released")
	}
	if got := payloadOf(t, recs[len(recs)-1]).DroppedWindows; got != dropped {
		t.Errorf("last record dropped_windows = %d, want %d", got, dropped)
	}
}

func TestFrameStatsCloseIsIdempotentAndStopsRecording(t *testing.T) {
	sink := &recordingSink{}
	f, _ := newTestFrameStats(sink)
	f.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
	closeFrameStats(t, f)
	closeFrameStats(t, f)
	f.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
	f.recordView(time.Microsecond)
	if got := len(sink.records()); got != 1 {
		t.Errorf("records = %d, want 1 (shutdown flush only)", got)
	}
	var nilStats *frameStats
	nilStats.Close(context.Background())
}

func TestModelFrameStatsEndToEnd(t *testing.T) {
	sink := &recordingSink{}
	frame, _ := newTestFrameStats(sink)
	m := newModel(Config{Model: "m"}, nil)
	m.frame = frame

	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 30},
		sidebarHoverMsg{},
		tea.KeyPressMsg{Code: 'a', Text: "a"},
		tea.MouseWheelMsg{},
	}
	for _, msg := range msgs {
		m.Update(msg)
		m.View()
	}
	closeFrameStats(t, frame)

	got := map[string]frameStatsPayload{}
	for _, rec := range sink.records() {
		p := payloadOf(t, rec)
		got[p.Type] = p
	}
	for _, typ := range []string{"window_size", "key", "mouse_raw", "other:tui.sidebarHoverMsg"} {
		p, ok := got[typ]
		if !ok {
			t.Errorf("no record for type %q, got %v", typ, got)
			continue
		}
		if p.UpdateCount != 1 || p.ViewCount != 1 {
			t.Errorf("%s counts = %d/%d, want 1/1", typ, p.UpdateCount, p.ViewCount)
		}
		if p.Width != 100 || p.Height != 30 {
			t.Errorf("%s dims = %dx%d, want 100x30", typ, p.Width, p.Height)
		}
	}
}

func TestModelWithoutFrameStatsUpdates(t *testing.T) {
	m := newModel(Config{Model: "m"}, nil)
	if m.frame != nil {
		t.Fatal("frame stats set without a sink")
	}
	m.Update(sidebarHoverMsg{})
	m.View()
}

func TestAppFrameStatsLifecycle(t *testing.T) {
	sink := &recordingSink{}
	app := &App{cfg: Config{FrameStats: sink}, bridge: newEventBridge(1)}
	p := app.NewProgram()
	if p == nil || app.frame == nil {
		t.Fatal("NewProgram did not create frame stats for a configured sink")
	}
	app.frame.recordUpdate(tickMsg{}, time.Microsecond, frameSnapshot{})
	app.Cleanup()
	if got := len(sink.records()); got != 1 {
		t.Errorf("records after Cleanup = %d, want the shutdown flush", got)
	}

	disabled := &App{cfg: Config{}, bridge: newEventBridge(1)}
	disabled.NewProgram()
	if disabled.frame != nil {
		t.Error("frame stats created without a sink")
	}
	disabled.Cleanup()
}

func BenchmarkUpdateFrameStatsOff(b *testing.B) {
	m := newModel(Config{Model: "bench"}, nil)
	msg := sidebarHoverMsg{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(msg)
	}
}

func BenchmarkUpdateFrameStatsOn(b *testing.B) {
	sink := &recordingSink{}
	m := newModel(Config{Model: "bench"}, nil)
	m.frame = newFrameStats(sink)
	msg := sidebarHoverMsg{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(msg)
	}
	b.StopTimer()
	m.frame.Close(context.Background())
}

// BenchmarkFrameStatsRecord isolates the per-message recording cost (Update
// plus View bookkeeping, snapshot excluded).
func BenchmarkFrameStatsRecord(b *testing.B) {
	f := newFrameStats(&recordingSink{})
	msg := sidebarHoverMsg{}
	snap := frameSnapshot{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.recordUpdate(msg, time.Microsecond, snap)
		f.recordView(time.Microsecond)
	}
}
