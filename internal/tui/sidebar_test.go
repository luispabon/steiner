package tui

import (
	"reflect"
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"
)

// TestSidebarStateComparableFieldParity pins sidebarStateComparable's field
// set against sidebarState's: every sidebarState field except modifiedFiles
// must appear in the projection with the same type, and the projection must
// not carry any extra fields. Without this, a field added to sidebarState
// later and forgotten in the projection would make renderSidebar's cache key
// silently incomplete — comparable() would compile and run fine, it just
// wouldn't notice the new field changed.
func TestSidebarStateComparableFieldParity(t *testing.T) {
	t.Parallel()

	stateType := reflect.TypeOf(sidebarState{})
	projType := reflect.TypeOf(sidebarStateComparable{})

	stateFields := map[string]reflect.Type{}
	for i := range stateType.NumField() {
		f := stateType.Field(i)
		if f.Name == "modifiedFiles" {
			continue
		}
		stateFields[f.Name] = f.Type
	}
	projFields := map[string]reflect.Type{}
	for i := range projType.NumField() {
		f := projType.Field(i)
		projFields[f.Name] = f.Type
	}

	for name, typ := range stateFields {
		projType, ok := projFields[name]
		if !ok {
			t.Errorf("sidebarState field %q (excluding modifiedFiles) is missing from sidebarStateComparable", name)
			continue
		}
		if projType != typ {
			t.Errorf("sidebarState.%s is %s but sidebarStateComparable.%s is %s", name, typ, name, projType)
		}
	}
	for name := range projFields {
		if _, ok := stateFields[name]; !ok {
			t.Errorf("sidebarStateComparable has field %q with no counterpart in sidebarState", name)
		}
	}

	// Positive control: the parity check is only meaningful if both walks
	// actually visited fields.
	if len(stateFields) == 0 || len(projFields) == 0 {
		t.Fatal("field parity walk covered zero fields on one side; test is vacuous")
	}
}

func newSidebarTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	m.sidebar.SetExpanded(true)
	return m
}

// TestRenderSidebarCachesOnIdenticalInput proves renderSidebar returns the
// exact cached string object (not a coincidentally-identical recomputation)
// when nothing about the sidebar has changed between calls.
func TestRenderSidebarCachesOnIdenticalInput(t *testing.T) {
	t.Parallel()
	m := newSidebarTestModel(t)

	first := m.renderSidebar(m.width, m.height)
	second := m.renderSidebar(m.width, m.height)
	if unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatalf("renderSidebar recomputed instead of returning the cached string\nfirst:  %q\nsecond: %q", first, second)
	}
	if !m.sidebarViewCacheSet {
		t.Fatal("sidebarViewCacheSet should be true after a render")
	}
}

// TestRenderSidebarInvalidation exercises the five required invalidation
// triggers: syncSidebar, a tick that changes tickCount, a width change, a
// height change, and an accent change. Each subtest warms the cache, applies
// exactly one trigger, and asserts the next render differs from the cached
// one (or, for width/height, differs because the key changed and produces a
// render matching a fresh direct call).
func TestRenderSidebarInvalidation(t *testing.T) {
	t.Parallel()

	t.Run("syncSidebar", func(t *testing.T) {
		t.Parallel()
		m := newSidebarTestModel(t)
		m.renderSidebar(m.width, m.height)
		cachedKey := m.sidebarViewCacheKey

		// syncSidebar unconditionally trims workingDir; give it untrimmed
		// input so the mutation is deterministic regardless of ambient git
		// repo state (branch/modifiedFiles depend on the real repo this test
		// runs in and may not change run to run).
		m.sidebar.workingDir = "  /tmp/example  "
		m.syncSidebar()
		if m.sidebar.workingDir != "/tmp/example" {
			t.Fatalf("syncSidebar should have trimmed workingDir, got %q", m.sidebar.workingDir)
		}

		got := m.renderSidebar(m.width, m.height)
		if m.sidebarViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after syncSidebar mutated sidebar state")
		}
		want := m.sidebar.View(m.width, m.height)
		if got != want {
			t.Fatalf("renderSidebar after syncSidebar does not match a fresh View()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("tick_changes_tickCount", func(t *testing.T) {
		t.Parallel()
		m := newSidebarTestModel(t)
		m.renderSidebar(m.width, m.height)
		cachedKey := m.sidebarViewCacheKey

		m = updateModelDirect(m, tickMsg{})

		got := m.renderSidebar(m.width, m.height)
		if m.sidebarViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after a tick advanced tickCount")
		}
		want := m.sidebar.View(m.width, m.height)
		if got != want {
			t.Fatalf("renderSidebar after tick does not match a fresh View()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("width_change", func(t *testing.T) {
		t.Parallel()
		m := newSidebarTestModel(t)
		// sidebarState.View only reads its width argument to gate visibility
		// (sidebarMinWidth); it does not use it to size the render. So the
		// width that actually produces an observably different render is one
		// that crosses the visibility threshold, not an arbitrary +1.
		narrowWidth := sidebarMinWidth - 1
		wideWidth := sidebarMinWidth + 10
		m.renderSidebar(narrowWidth, m.height)

		got := m.renderSidebar(wideWidth, m.height)
		want := m.sidebar.View(wideWidth, m.height)
		if got != want {
			t.Fatalf("renderSidebar after width change does not match a fresh View()\ngot:  %q\nwant: %q", got, want)
		}
		if got == "" {
			t.Fatal("expected a non-empty render once width crosses sidebarMinWidth")
		}
	})

	t.Run("height_change", func(t *testing.T) {
		t.Parallel()
		m := newSidebarTestModel(t)
		m.renderSidebar(m.width, m.height)

		got := m.renderSidebar(m.width, m.height+1)
		want := m.sidebar.View(m.width, m.height+1)
		if got != want {
			t.Fatalf("renderSidebar after height change does not match a fresh View()\ngot:  %q\nwant: %q", got, want)
		}
	})

	t.Run("accent_change", func(t *testing.T) {
		t.Parallel()
		m := newSidebarTestModel(t)
		m.renderSidebar(m.width, m.height)
		cachedKey := m.sidebarViewCacheKey

		m = updateModelDirect(m, setAccentMsg{preset: "violet"})

		got := m.renderSidebar(m.width, m.height)
		if m.sidebarViewCacheKey == cachedKey {
			t.Fatal("cache key did not change after an accent change re-pointed styles")
		}
		want := m.sidebar.View(m.width, m.height)
		if got != want {
			t.Fatalf("renderSidebar after accent change does not match a fresh View()\ngot:  %q\nwant: %q", got, want)
		}
	})
}

// setDistinctValue writes a non-zero, deterministic value into an unexported
// struct field so the projection assertion below can tell "copied" from
// "left at the zero value". reflect cannot Set unexported fields directly, so
// the address is re-derived through unsafe; this is confined to tests.
func setDistinctValue(t *testing.T, f reflect.Value, seed int) {
	t.Helper()
	w := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	switch f.Kind() {
	case reflect.Bool:
		w.SetBool(true)
	case reflect.String:
		w.SetString("distinct-" + string(rune('a'+seed%26)))
	case reflect.Int, reflect.Int64:
		w.SetInt(int64(seed + 1))
	case reflect.Float64:
		w.SetFloat(float64(seed) + 1.5)
	case reflect.Pointer:
		w.Set(reflect.New(f.Type().Elem()))
	case reflect.Struct:
		for i := range f.NumField() {
			setDistinctValue(t, f.Field(i), seed+i)
		}
	default:
		t.Fatalf("setDistinctValue: unhandled kind %s; extend this switch", f.Kind())
	}
}

// TestSidebarComparableCopiesEveryField closes the gap left by
// TestSidebarStateComparableFieldParity. Parity proves the two structs
// declare the same field set; it does NOT prove comparable() assigns each
// one. A field declared in both but omitted from the comparable() literal
// stays at its zero value in every key, so writes to it never invalidate the
// sidebar cache and the sidebar renders stale forever — which compiles, and
// which the parity test passes.
//
// Verified by mutation: deleting `branch: s.branch` from comparable() makes
// this test fail and every other test in the package still pass.
func TestSidebarComparableCopiesEveryField(t *testing.T) {
	t.Parallel()

	var s sidebarState
	sv := reflect.ValueOf(&s).Elem()
	for i := range sv.NumField() {
		if sv.Type().Field(i).Name == "modifiedFiles" {
			continue
		}
		setDistinctValue(t, sv.Field(i), i)
	}

	projVal := s.comparable()
	proj := reflect.ValueOf(&projVal).Elem() // addressable, so unexported fields can be read
	checked := 0
	for i := range proj.NumField() {
		name := proj.Type().Field(i).Name
		src := sv.FieldByName(name)
		if !src.IsValid() {
			continue // parity test owns this failure mode
		}
		got := proj.Field(i)
		if !reflect.DeepEqual(
			reflect.NewAt(got.Type(), unsafe.Pointer(got.UnsafeAddr())).Elem().Interface(),
			reflect.NewAt(src.Type(), unsafe.Pointer(src.UnsafeAddr())).Elem().Interface(),
		) {
			t.Errorf("sidebarState.comparable() does not copy field %q: "+
				"projection holds the zero value while the source is set. Writes to "+
				"%s will not invalidate the sidebar render cache, serving a stale sidebar.",
				name, name)
		}
		checked++
	}

	// Positive control: an empty projection would pass the loop vacuously.
	if checked == 0 {
		t.Fatal("checked no projection fields; the test asserts nothing")
	}
}
