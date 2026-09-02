package tui

import (
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"
)

func TestToolCallDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{name: "specialized type", tool: "sub_agent", args: map[string]any{"type": "code"}, want: "code"},
		{name: "specialized type is normalized", tool: "sub_agent", args: map[string]any{"type": " Code "}, want: "code"},
		{name: "missing type", tool: "sub_agent", want: "sub_agent"},
		{name: "non-string type", tool: "sub_agent", args: map[string]any{"type": 42}, want: "sub_agent"},
		{name: "blank type", tool: "sub_agent", args: map[string]any{"type": "  "}, want: "sub_agent"},
		{name: "ordinary tool ignores type", tool: "read", args: map[string]any{"type": "code"}, want: "read"},
		{name: "tool is trimmed", tool: "  read  ", args: map[string]any{"type": "code"}, want: "read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := toolCallDetail(tt.tool, tt.args); got != tt.want {
				t.Fatalf("toolCallDetail(%q, %#v) = %q, want %q", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func newActivityTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	return m
}

// TestRenderActivityRowCachesOnIdenticalInput proves renderActivityRow
// returns the exact cached string object (not a coincidentally-identical
// recomputation) when nothing about the activity row has changed.
func TestRenderActivityRowCachesOnIdenticalInput(t *testing.T) {
	t.Parallel()
	m := newActivityTestModel(t)
	m.activity = m.activity.waiting("running tool", "read")

	first := m.renderActivityRow(m.contentWidth())
	second := m.renderActivityRow(m.contentWidth())
	if unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatalf("renderActivityRow recomputed instead of returning the cached string\nfirst:  %q\nsecond: %q", first, second)
	}
	if !m.activityViewCacheSet {
		t.Fatal("activityViewCacheSet should be true after a render")
	}
}

// TestRenderActivityRowInvalidation exercises every input renderActivityRow
// reads: label, detail, spinning, the spinner's advancing frame, width, and
// an accent (styles pointer) change. Each subtest warms the cache, applies
// one trigger, and asserts the next render matches a fresh direct call
// rather than the stale cached one.
func TestRenderActivityRowInvalidation(t *testing.T) {
	t.Parallel()

	t.Run("label_change", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.activity = m.activity.static("first label", "")
		m.renderActivityRow(m.contentWidth())

		m.activity = m.activity.static("second label", "")
		got := m.renderActivityRow(m.contentWidth())
		want := m.activity.view(m.contentWidth(), m.styles)
		if got != want {
			t.Fatalf("renderActivityRow after label change does not match a fresh view()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("detail_change", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.activity = m.activity.static("label", "first detail")
		m.renderActivityRow(m.contentWidth())

		m.activity = m.activity.static("label", "second detail")
		got := m.renderActivityRow(m.contentWidth())
		want := m.activity.view(m.contentWidth(), m.styles)
		if got != want {
			t.Fatalf("renderActivityRow after detail change does not match a fresh view()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("spinning_change", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.activity = m.activity.static("label", "detail")
		m.renderActivityRow(m.contentWidth())

		m.activity = m.activity.waiting("label", "detail")
		got := m.renderActivityRow(m.contentWidth())
		want := m.activity.view(m.contentWidth(), m.styles)
		if got != want {
			t.Fatalf("renderActivityRow after spinning change does not match a fresh view()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("spinner_frame_advances_on_tick", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.activity = m.activity.waiting("label", "detail")
		m.renderActivityRow(m.contentWidth())
		cachedKey := m.activityViewCacheKey

		m = updateModelDirect(m, tickMsg{})

		got := m.renderActivityRow(m.contentWidth())
		if m.activityViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after a tick advanced the spinner frame")
		}
		want := m.activity.view(m.contentWidth(), m.styles)
		if got != want {
			t.Fatalf("renderActivityRow after tick does not match a fresh view()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("width_change", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.activity = m.activity.static("label", "detail")
		m.renderActivityRow(m.contentWidth())

		got := m.renderActivityRow(m.contentWidth() + 5)
		want := m.activity.view(m.contentWidth()+5, m.styles)
		if got != want {
			t.Fatalf("renderActivityRow after width change does not match a fresh view()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("accent_change", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.activity = m.activity.static("label", "detail")
		m.renderActivityRow(m.contentWidth())
		cachedKey := m.activityViewCacheKey

		m = updateModelDirect(m, setAccentMsg{preset: "violet"})

		got := m.renderActivityRow(m.contentWidth())
		if m.activityViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after an accent change re-pointed styles")
		}
		want := m.activity.view(m.contentWidth(), m.styles)
		if got != want {
			t.Fatalf("renderActivityRow after accent change does not match a fresh view()\ngot:  %q\nwant: %q", got, want)
		}
	})
}
