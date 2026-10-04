package tui

import (
	"context"
	"reflect"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/diagnostics"
)

const (
	frameStatsWindow = 10 * time.Second
	// frameStatsQueue bounds finished windows waiting for the writer goroutine.
	frameStatsQueue = 8
	// frameHistBuckets is the number of log buckets; the last is open-ended.
	frameHistBuckets = 15
)

// frameHistBounds holds the bucket upper bounds in nanoseconds for every
// bucket except the last (1024 ms and above).
var frameHistBounds = [frameHistBuckets - 1]int64{
	50_000, 100_000, 250_000, 500_000,
	1_000_000, 2_000_000, 4_000_000, 8_000_000, 16_000_000, 32_000_000,
	64_000_000, 128_000_000, 256_000_000, 512_000_000,
}

// FrameStatsSink receives finished frame-stats records. *diagnostics.Writer
// satisfies it. A nil sink disables frame stats.
type FrameStatsSink interface {
	Write(rec diagnostics.Record)
}

type frameStatsKey struct{ name, sub string }

func (k frameStatsKey) String() string {
	if k.sub == "" {
		return k.name
	}
	return k.name + ":" + k.sub
}

// classifyMsg maps a message to a short, bounded-cardinality type name.
func classifyMsg(msg tea.Msg) frameStatsKey {
	if key, ok := classifyMouseMsg(msg); ok {
		return key
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return frameStatsKey{name: "key"}
	case tickMsg:
		return frameStatsKey{name: "tick"}
	case syncDebounceFiredMsg:
		return frameStatsKey{name: "sync_debounce"}
	case runtimeEventMsg:
		return frameStatsKey{name: "runtime_event", sub: msg.Event.Type}
	case tea.WindowSizeMsg:
		return frameStatsKey{name: "window_size"}
	case composerBlinkMsg:
		return frameStatsKey{name: "composer_blink"}
	case sessionTickMsg:
		return frameStatsKey{name: "session_tick"}
	case dragAutoScrollTickMsg:
		return frameStatsKey{name: "drag_autoscroll"}
	case jumpFlashTickMsg:
		return frameStatsKey{name: "jump_flash"}
	case tea.PasteMsg:
		return frameStatsKey{name: "paste"}
	default:
		return frameStatsKey{name: "other", sub: reflect.TypeOf(msg).String()}
	}
}

// classifyMouseMsg names the internal mouse messages and, distinctly, the raw
// tea.MouseMsg values that still reach Update alongside them.
func classifyMouseMsg(msg tea.Msg) (frameStatsKey, bool) {
	switch msg.(type) {
	case mouseWheelMsg:
		return frameStatsKey{name: "mouse_wheel"}, true
	case mouseClickMsg:
		return frameStatsKey{name: "mouse_click"}, true
	case mouseReleaseMsg:
		return frameStatsKey{name: "mouse_release"}, true
	case mouseMotionMsg:
		return frameStatsKey{name: "mouse_motion"}, true
	case tea.MouseMsg:
		return frameStatsKey{name: "mouse_raw"}, true
	}
	return frameStatsKey{}, false
}

func histBucket(ns int64) int {
	for i, bound := range frameHistBounds {
		if ns <= bound {
			return i
		}
	}
	return frameHistBuckets - 1
}

type frameCost struct {
	count int
	sumNs int64
	maxNs int64
	hist  [frameHistBuckets]int
}

func (c *frameCost) add(d time.Duration) {
	ns := d.Nanoseconds()
	c.count++
	c.sumNs += ns
	c.maxNs = max(c.maxNs, ns)
	c.hist[histBucket(ns)]++
}

type frameAgg struct{ update, view frameCost }

// frameSnapshot is the model state sampled after each Update.
type frameSnapshot struct {
	overlayOpen     bool
	subagentsActive int
	transcriptLines int
	width, height   int
}

// frameStatsPayload is the fixed-size record payload for one message type in
// one window. Window-level fields repeat on every record of a window.
type frameStatsPayload struct {
	Type                string                `json:"type"`
	WindowID            int                   `json:"window_id"`
	WindowMs            float64               `json:"window_ms"`
	UpdateCount         int                   `json:"update_count"`
	UpdateSumMs         float64               `json:"update_sum_ms"`
	UpdateMaxMs         float64               `json:"update_max_ms"`
	UpdateHist          [frameHistBuckets]int `json:"update_hist"`
	ViewCount           int                   `json:"view_count"`
	ViewSumMs           float64               `json:"view_sum_ms"`
	ViewMaxMs           float64               `json:"view_max_ms"`
	ViewHist            [frameHistBuckets]int `json:"view_hist"`
	OverlayOpenMs       float64               `json:"overlay_open_ms"`
	SubagentsRunningMax int                   `json:"subagents_running_max"`
	TranscriptLines     int                   `json:"transcript_lines"`
	Width               int                   `json:"width"`
	Height              int                   `json:"height"`
	DroppedWindows      int                   `json:"dropped_windows"`
}

// frameStats aggregates per-message Update/View cost into fixed windows and
// hands finished windows to a goroutine so the event loop never does file I/O.
// All methods except the writer goroutine must run on the event loop (or after
// it has exited, for Close).
type frameStats struct {
	now  func() time.Time
	ch   chan []diagnostics.Record
	done chan struct{}

	winStart    time.Time
	winID       int
	aggs        map[frameStatsKey]*frameAgg
	pending     *frameAgg
	last        frameSnapshot
	lastSample  time.Time
	overlayDur  time.Duration
	subagentMax int
	dropped     int
	closed      bool
}

func newFrameStats(sink FrameStatsSink) *frameStats {
	f := &frameStats{
		now:  time.Now,
		ch:   make(chan []diagnostics.Record, frameStatsQueue),
		done: make(chan struct{}),
		aggs: make(map[frameStatsKey]*frameAgg),
	}
	go func() {
		defer close(f.done)
		for batch := range f.ch {
			for _, rec := range batch {
				sink.Write(rec)
			}
		}
	}()
	return f
}

func (f *frameStats) agg(key frameStatsKey) *frameAgg {
	a := f.aggs[key]
	if a == nil {
		a = &frameAgg{}
		f.aggs[key] = a
	}
	return a
}

// recordUpdate files one Update's cost and samples the post-Update state.
func (f *frameStats) recordUpdate(msg tea.Msg, d time.Duration, snap frameSnapshot) {
	if f.closed {
		return
	}
	now := f.now()
	if f.winStart.IsZero() {
		f.winStart, f.lastSample = now.Add(-d), now
	} else if now.Sub(f.winStart) >= frameStatsWindow {
		f.flush(now)
	}
	if f.last.overlayOpen {
		f.overlayDur += now.Sub(f.lastSample)
	}
	f.lastSample, f.last = now, snap
	f.subagentMax = max(f.subagentMax, snap.subagentsActive)
	a := f.agg(classifyMsg(msg))
	a.update.add(d)
	f.pending = a
}

// recordView files the View that followed the most recent Update.
func (f *frameStats) recordView(d time.Duration) {
	if f.closed {
		return
	}
	a := f.pending
	if a == nil {
		a = f.agg(frameStatsKey{name: "view_unpaired"})
	}
	f.pending = nil
	a.view.add(d)
}

func ms(ns int64) float64 { return float64(ns) / 1e6 }

// flush ends the current window at now and queues one record per non-empty
// message type. A full queue drops the window and counts it.
func (f *frameStats) flush(now time.Time) {
	if f.winStart.IsZero() {
		return
	}
	if f.last.overlayOpen {
		f.overlayDur += now.Sub(f.lastSample)
	}
	f.lastSample = now
	keys := make([]frameStatsKey, 0, len(f.aggs))
	for k, a := range f.aggs {
		if a.update.count+a.view.count > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	f.winID++
	recs := make([]diagnostics.Record, 0, len(keys))
	for _, k := range keys {
		a := f.aggs[k]
		recs = append(recs, diagnostics.Record{
			Timestamp: now.UTC(),
			Kind:      diagnostics.KindTUI,
			Payload: frameStatsPayload{
				Type:                k.String(),
				WindowID:            f.winID,
				WindowMs:            ms(now.Sub(f.winStart).Nanoseconds()),
				UpdateCount:         a.update.count,
				UpdateSumMs:         ms(a.update.sumNs),
				UpdateMaxMs:         ms(a.update.maxNs),
				UpdateHist:          a.update.hist,
				ViewCount:           a.view.count,
				ViewSumMs:           ms(a.view.sumNs),
				ViewMaxMs:           ms(a.view.maxNs),
				ViewHist:            a.view.hist,
				OverlayOpenMs:       ms(f.overlayDur.Nanoseconds()),
				SubagentsRunningMax: f.subagentMax,
				TranscriptLines:     f.last.transcriptLines,
				Width:               f.last.width,
				Height:              f.last.height,
				DroppedWindows:      f.dropped,
			},
		})
	}
	if len(recs) > 0 {
		select {
		case f.ch <- recs:
		default:
			f.dropped++
		}
	}
	clear(f.aggs)
	f.pending = nil
	f.winStart = now
	f.overlayDur = 0
	f.subagentMax = f.last.subagentsActive
}

// Close flushes the open window and waits for the writer goroutine to drain,
// bounded by ctx. Safe to call more than once and on a nil receiver.
func (f *frameStats) Close(ctx context.Context) {
	if f == nil || f.closed {
		return
	}
	f.flush(f.now())
	f.closed = true
	close(f.ch)
	select {
	case <-f.done:
	case <-ctx.Done():
	}
}

// snapshotFrame samples the state frameStats reports on.
func (m *Model) snapshotFrame() frameSnapshot {
	return frameSnapshot{
		overlayOpen:     m.anyOverlayOpen(),
		subagentsActive: len(m.content.activeDelegations),
		transcriptLines: m.viewport.TotalLineCount(),
		width:           m.width,
		height:          m.height,
	}
}
