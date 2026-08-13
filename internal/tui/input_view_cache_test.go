package tui

import (
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"
)

func newInputViewTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	return updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
}

// TestRenderInputViewCachesOnIdenticalInput proves renderInputView returns the
// exact cached string object (not a coincidentally-identical recomputation)
// when nothing the input render path reads has changed between calls.
func TestRenderInputViewCachesOnIdenticalInput(t *testing.T) {
	t.Parallel()
	m := newInputViewTestModel(t)
	m.input.SetValue("hello world")
	m.input.CursorEnd()

	first := m.renderInputView(m.contentWidth())
	second := m.renderInputView(m.contentWidth())
	if unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatalf("renderInputView recomputed instead of returning the cached string\nfirst:  %q\nsecond: %q", first, second)
	}
	if !m.inputViewCacheSet {
		t.Fatal("inputViewCacheSet should be true after a render")
	}
}

// TestRenderInputViewInvalidation exercises every key input of the composer
// render cache. Each subtest warms the cache, applies exactly one mutation,
// and asserts the cache key changed AND the next render matches a fresh
// uncached render (the stale-frame guard).
func TestRenderInputViewInvalidation(t *testing.T) {
	t.Parallel()

	t.Run("width_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("hello world")

		w1 := m.contentWidth()
		m.renderInputView(w1)
		cachedKey := m.inputViewCacheKey

		w2 := w1 - 10
		got := m.renderInputView(w2)
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after contentWidth changed")
		}
		want := m.renderInputViewUncached(w2)
		if got != want {
			t.Fatalf("renderInputView after width change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("height_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("hello world")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 24})

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after height changed")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after height change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("value_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("hello world")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		// Same length as the warmed value so SetValue leaves the cursor at the
		// same position: the only changed key input is value itself.
		m.input.SetValue("hello there")

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after input value changed")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after value change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("cursor_line_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("alpha\nbeta")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		// SetValue leaves the cursor at (1,4) (end of last line); CursorUp moves
		// the line to 0 while the column stays 4, so only cursorLine changes.
		m.input.CursorUp()
		if m.input.Line() == cachedKey.cursorLine && m.input.Column() == cachedKey.cursorColumn {
			t.Fatal("CursorUp did not move the cursor; subtest cannot exercise cursor invalidation")
		}

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after the cursor line moved")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after cursor line move does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("cursor_column_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("alpha\nbeta")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		// SetValue leaves the cursor at (1,4); SetCursorColumn moves only the
		// column, so only cursorColumn changes.
		m.input.SetCursorColumn(2)
		if m.input.Line() == cachedKey.cursorLine && m.input.Column() == cachedKey.cursorColumn {
			t.Fatal("SetCursorColumn did not move the cursor; subtest cannot exercise cursor invalidation")
		}

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after the cursor column moved")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after cursor column move does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("placeholder_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		m.input.Placeholder = "a different placeholder"

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after placeholder changed")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after placeholder change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("oneshot_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("/exit")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		m.oneshotRunning = true

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after oneshotRunning changed")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after oneshotRunning change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("skillNames_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("/skillx")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey
		cachedOutput := m.inputViewCacheRendered

		m.skillNames = []string{"skillx"}

		got := m.renderInputView(m.contentWidth())
		// skillNames is compared via the separate slices.Equal check, so the
		// struct key itself must be unchanged; the miss has to come from the
		// slice comparison.
		if m.inputViewCacheKey != cachedKey {
			t.Fatal("struct cache key changed; expected the skillNames miss to come from the slices comparison")
		}
		if got == cachedOutput {
			t.Fatal("renderInputView returned the stale cached output after skillNames changed")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after skillNames change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("accent_change", func(t *testing.T) {
		t.Parallel()
		m := newInputViewTestModel(t)
		m.input.SetValue("hello world")
		m.renderInputView(m.contentWidth())
		cachedKey := m.inputViewCacheKey

		m = updateModelDirect(m, setAccentMsg{preset: "violet"})

		got := m.renderInputView(m.contentWidth())
		if m.inputViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after an accent change re-pointed styles")
		}
		want := m.renderInputViewUncached(m.contentWidth())
		if got != want {
			t.Fatalf("renderInputView after accent change does not match a fresh render\ngot:  %q\nwant: %q", got, want)
		}
	})
}

// TestRenderInputViewSkillNamesDefensiveCopy proves the cache stores a copy of
// skillNames, not an alias: mutating the live slice in place (element write and
// append into spare capacity, both of which would corrupt an aliased copy) must
// leave inputViewCacheSkills untouched.
func TestRenderInputViewSkillNamesDefensiveCopy(t *testing.T) {
	t.Parallel()
	m := newInputViewTestModel(t)
	m.input.SetValue("/skillx")
	m.skillNames = make([]string, 1, 4)
	m.skillNames[0] = "skillx"
	m.renderInputView(m.contentWidth())

	m.skillNames[0] = "mutated"
	m.skillNames = append(m.skillNames, "skill2") // writes into the shared backing array

	if len(m.inputViewCacheSkills) != 1 || m.inputViewCacheSkills[0] != "skillx" {
		t.Fatalf("inputViewCacheSkills aliases skillNames; got %q, want [skillx]", m.inputViewCacheSkills)
	}
}

func newVDividerTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	m.sidebar.SetExpanded(true)
	return m
}

// TestVDividerCachedOnIdenticalFrame proves the vertical divider render is
// memoized across identical frames and matches a fresh compute.
func TestVDividerCachedOnIdenticalFrame(t *testing.T) {
	t.Parallel()
	m := newVDividerTestModel(t)

	m.renderBaseView(m.contentWidth(), true)
	first := unsafe.StringData(m.vDividerCacheRendered)
	m.renderBaseView(m.contentWidth(), true)
	second := unsafe.StringData(m.vDividerCacheRendered)

	if m.vDividerCacheRendered == "" {
		t.Fatal("vDividerCacheRendered should be non-empty after a render")
	}
	if first != second {
		t.Fatal("vDividerCacheRendered changed between identical frames")
	}
	want := m.styles.VDivider.Height(m.height).Render("")
	if m.vDividerCacheRendered != want {
		t.Fatalf("vDividerCacheRendered does not match a fresh compute\ngot:  %q\nwant: %q", m.vDividerCacheRendered, want)
	}
}

// TestVDividerInvalidation exercises the two inputs of the vDivider cache:
// height and the styles pointer (accent change).
func TestVDividerInvalidation(t *testing.T) {
	t.Parallel()

	t.Run("height_change", func(t *testing.T) {
		t.Parallel()
		m := newVDividerTestModel(t)
		m.renderBaseView(m.contentWidth(), true)
		before := m.vDividerCacheRendered

		m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 40})
		m.renderBaseView(m.contentWidth(), true)

		if unsafe.StringData(before) == unsafe.StringData(m.vDividerCacheRendered) {
			t.Fatal("vDividerCacheRendered did not change after height changed")
		}
		want := m.styles.VDivider.Height(m.height).Render("")
		if m.vDividerCacheRendered != want {
			t.Fatalf("vDividerCacheRendered after height change does not match a fresh compute\ngot:  %q\nwant: %q", m.vDividerCacheRendered, want)
		}
	})

	t.Run("accent_change", func(t *testing.T) {
		t.Parallel()
		m := newVDividerTestModel(t)
		m.renderBaseView(m.contentWidth(), true)
		before := m.vDividerCacheRendered

		m = updateModelDirect(m, setAccentMsg{preset: "violet"})
		m.renderBaseView(m.contentWidth(), true)

		if unsafe.StringData(before) == unsafe.StringData(m.vDividerCacheRendered) {
			t.Fatal("vDividerCacheRendered did not change after an accent change re-pointed styles")
		}
		if m.vDividerCacheStyles != m.styles {
			t.Fatal("vDividerCacheStyles should track the current styles pointer after an accent change")
		}
		want := m.styles.VDivider.Height(m.height).Render("")
		if m.vDividerCacheRendered != want {
			t.Fatalf("vDividerCacheRendered after accent change does not match a fresh compute\ngot:  %q\nwant: %q", m.vDividerCacheRendered, want)
		}
	})
}
