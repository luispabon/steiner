package tui

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// assertRenderKeyParity fails when a field of state has no same-named, same-typed
// field in key (slice fields are keyed by sliceID of their element type) and is
// not listed in ignored with a reason, or when key carries a field state lacks.
func assertRenderKeyParity(t *testing.T, state, key any, ignored map[string]string) {
	t.Helper()
	st, kt := reflect.TypeOf(state), reflect.TypeOf(key)
	keyFields := map[string]reflect.Type{}
	for i := range kt.NumField() {
		f := kt.Field(i)
		keyFields[f.Name] = f.Type
	}
	visited := 0
	for i := range st.NumField() {
		f := st.Field(i)
		if _, skip := ignored[f.Name]; skip {
			continue
		}
		visited++
		kf, ok := keyFields[f.Name]
		if !ok {
			t.Errorf("%s field %q is missing from %s; add it to the key or to the ignored list with a reason", st.Name(), f.Name, kt.Name())
			continue
		}
		want := f.Type
		if f.Type.Kind() == reflect.Slice {
			if kf.Kind() != reflect.Struct || kf.NumField() != 2 || kf.Field(0).Type != reflect.PointerTo(f.Type.Elem()) {
				t.Errorf("%s.%s is %s but %s.%s is %s, want sliceID of the element type", st.Name(), f.Name, f.Type, kt.Name(), f.Name, kf)
			}
			continue
		}
		if kf != want {
			t.Errorf("%s.%s is %s but %s.%s is %s", st.Name(), f.Name, want, kt.Name(), f.Name, kf)
		}
	}
	for name := range keyFields {
		if _, ok := st.FieldByName(name); !ok {
			t.Errorf("%s has field %q with no counterpart in %s", kt.Name(), name, st.Name())
		}
	}
	if visited == 0 || len(keyFields) == 0 {
		t.Fatal("parity walk covered zero fields; test is vacuous")
	}
}

// perturb changes v to a value different from its current one.
func perturb(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Int:
		v.SetInt(v.Int() + 7)
	case reflect.String:
		v.SetString(v.String() + "x")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 3, 3))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Field(i)
			perturb(t, reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem())
		}
	default:
		t.Fatalf("perturb: unsupported kind %s", v.Kind())
	}
}

// assertRenderKeyCopiesEveryField fails when changing any non-ignored field of
// a state leaves its key unchanged. Parity alone cannot see a field that is
// declared in the key but never assigned.
func assertRenderKeyCopiesEveryField[S any](t *testing.T, base S, ignored map[string]string, key func(S) any) {
	t.Helper()
	typ := reflect.TypeOf(base)
	want := key(base)
	checked := 0
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if _, skip := ignored[name]; skip {
			continue
		}
		s := base
		sv := reflect.ValueOf(&s).Elem()
		f := sv.Field(i)
		perturb(t, reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem())
		if key(s) == want {
			t.Errorf("changing %s.%s does not change the render key; the memo would serve a stale render", typ.Name(), name)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("checked no fields; test is vacuous")
	}
}

func TestOverlayRenderKeys(t *testing.T) {
	t.Parallel()
	styles := newModel(Config{}, nil).styles

	t.Run("slash", func(t *testing.T) {
		t.Parallel()
		ign := map[string]string{"allItems": "View renders candidates, not the full list"}
		assertRenderKeyParity(t, slashOverlay{}, slashRenderKey{}, ign)
		base := slashOverlay{styles: styles}
		assertRenderKeyCopiesEveryField(t, base, ign, func(s slashOverlay) any { return s.renderKey() })
	})
	t.Run("filepicker", func(t *testing.T) {
		t.Parallel()
		ign := map[string]string{"root": "not rendered", "allEntries": "View renders candidates"}
		assertRenderKeyParity(t, filePickerOverlay{}, filePickerRenderKey{}, ign)
		assertRenderKeyCopiesEveryField(t, filePickerOverlay{styles: styles}, ign, func(s filePickerOverlay) any { return s.renderKey() })
	})
	t.Run("modelpicker", func(t *testing.T) {
		t.Parallel()
		ign := map[string]string{"allEntries": "View renders candidates"}
		assertRenderKeyParity(t, modelPickerOverlay{}, modelPickerRenderKey{}, ign)
		assertRenderKeyCopiesEveryField(t, modelPickerOverlay{styles: styles}, ign, func(s modelPickerOverlay) any { return s.renderKey() })
	})
	t.Run("filelist", func(t *testing.T) {
		t.Parallel()
		assertRenderKeyParity(t, fileListOverlay{}, fileListRenderKey{}, nil)
		assertRenderKeyCopiesEveryField(t, fileListOverlay{styles: styles}, nil, func(s fileListOverlay) any { return s.renderKey() })
	})
	t.Run("mcp", func(t *testing.T) {
		t.Parallel()
		ign := map[string]string{"servers": "flattened into lines", "mcpEnabled": "flattened into lines"}
		assertRenderKeyParity(t, mcpOverlay{}, mcpRenderKey{}, ign)
		assertRenderKeyCopiesEveryField(t, mcpOverlay{styles: styles}, ign, func(s mcpOverlay) any { return s.renderKey() })
	})
	t.Run("lsp", func(t *testing.T) {
		t.Parallel()
		ign := map[string]string{"sessions": "flattened into lines", "lspEnabled": "flattened into lines"}
		assertRenderKeyParity(t, lspOverlay{}, lspRenderKey{}, ign)
		assertRenderKeyCopiesEveryField(t, lspOverlay{styles: styles}, ign, func(s lspOverlay) any { return s.renderKey() })
	})
	t.Run("context", func(t *testing.T) {
		t.Parallel()
		ign := map[string]string{
			"content":           "already reflowed into renderedLines",
			"lineCount":         "len(renderedLines)",
			"glamourStyleSheet": "only used by reflow",
			"renderer":          "only used by reflow",
			"renderWidth":       "only used by reflow",
			"styles":            "the render reads Model.styles, keyed separately",
		}
		assertRenderKeyParity(t, contextOverlayState{}, contextRenderKey{}, ign)
		assertRenderKeyCopiesEveryField(t, contextOverlayState{}, ign, func(s contextOverlayState) any {
			m := &Model{styles: styles, contextOverlay: s}
			return m.contextRenderKey()
		})
		a, b := &Model{styles: styles}, &Model{styles: newModel(Config{}, nil).styles}
		if a.contextRenderKey() == b.contextRenderKey() {
			t.Error("context render key ignores Model.styles")
		}
		w := &Model{styles: styles, width: 10}
		if a.contextRenderKey() == w.contextRenderKey() {
			t.Error("context render key ignores Model dimensions")
		}
	})
}

// memoSentinel replaces a memoised string so tests can see whether it was reused.
const memoSentinel = "memo-sentinel"

type memoCase struct {
	name string
	open func(t *testing.T, m *Model)
	// frame renders the overlay through the memoised path, view through the plain one.
	frame func(m *Model) string
	view  func(m *Model) string
	// poison overwrites the memoised string so a reuse is observable.
	poison    func(m *Model)
	mutations []memoMutation
}

type memoMutation struct {
	name    string
	apply   func(t *testing.T, m *Model) *Model
	changes bool
}

func keyMutation(name string, msg tea.Msg, changes bool) memoMutation {
	return memoMutation{name: name, changes: changes, apply: func(t *testing.T, m *Model) *Model { return updateModel(t, m, msg) }}
}

// commonMemoMutations are the model-wide changes every overlay must survive.
// The flags say whether the overlay's own output is expected to change; the
// memoised render is compared with the uncached one either way.
func commonMemoMutations(resizeChanges, accentChanges bool) []memoMutation {
	return []memoMutation{
		{name: "resize", changes: resizeChanges, apply: func(t *testing.T, m *Model) *Model {
			return updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})
		}},
		{name: "accent", changes: accentChanges, apply: func(t *testing.T, m *Model) *Model {
			return updateModel(t, m, setAccentMsg{preset: "violet"})
		}},
	}
}

func memoCases() []memoCase {
	return []memoCase{
		{
			name: "slash",
			open: func(_ *testing.T, m *Model) {
				m.input.SetValue("/c")
				m.input.CursorEnd()
				m.slashOverlay = m.slashOverlay.Open(m.buildSlashOverlayItems())
				m.slashOverlay.width, m.slashOverlay.height = m.width, m.height
				m.syncSlashOverlayWithComposer()
			},
			frame:  func(m *Model) string { return m.bottomOverlayView(&m.slashOverlay) },
			view:   func(m *Model) string { return m.slashOverlay.View() },
			poison: func(m *Model) { m.overlayMemos.slash.out = memoSentinel },
			mutations: append([]memoMutation{
				keyMutation("down", benchKeyDown, true),
				keyMutation("type", benchRune('o'), true),
				keyMutation("backspace", benchBackspc, true),
			}, commonMemoMutations(true, true)...),
		},
		{
			name: "filepicker",
			open: func(t *testing.T, m *Model) {
				m.input.SetValue("@s")
				m.input.CursorEnd()
				f := m.filePicker.Open(t.TempDir())
				f.allEntries = benchFilePaths(60)
				f.candidates = append([]string(nil), f.allEntries...)
				f.matchIndexes = make([][]int, len(f.candidates))
				f.width, f.height = m.width, m.height
				m.filePicker = f
				m.syncFilePickerWithComposer()
			},
			frame:  func(m *Model) string { return m.bottomOverlayView(&m.filePicker) },
			view:   func(m *Model) string { return m.filePicker.View() },
			poison: func(m *Model) { m.overlayMemos.filePick.out = memoSentinel },
			mutations: append([]memoMutation{
				keyMutation("down", benchKeyDown, true),
				keyMutation("type", benchRune('e'), true),
				keyMutation("backspace", benchBackspc, true),
			}, commonMemoMutations(true, true)...),
		},
		{
			name: "modelpicker",
			open: func(_ *testing.T, m *Model) {
				m.modelEntries = benchModelEntries(40)
				m.executeOpenModelPickerAction()
			},
			frame:  func(m *Model) string { return m.bottomOverlayView(&m.modelPicker) },
			view:   func(m *Model) string { return m.modelPicker.View() },
			poison: func(m *Model) { m.overlayMemos.modelPick.out = memoSentinel },
			mutations: append([]memoMutation{
				keyMutation("down", benchKeyDown, true),
				keyMutation("type", benchRune('o'), true),
				keyMutation("backspace", benchBackspc, true),
			}, commonMemoMutations(false, true)...),
		},
		{
			name: "filelist",
			open: func(t *testing.T, m *Model) {
				m.executeListFilesAction(t.TempDir())
				m.fileList.entries = benchFilePaths(60)
			},
			frame:     func(m *Model) string { return m.exclusiveOverlayView()() },
			view:      func(m *Model) string { return m.fileList.View() },
			poison:    func(m *Model) { m.overlayMemos.fileList.out = memoSentinel },
			mutations: commonMemoMutations(true, true),
		},
		{
			name: "mcp",
			open: func(_ *testing.T, m *Model) {
				m.mcpServers, m.mcpEnabled = benchMCPServers(), true
				m.executeShowMCPAction()
			},
			frame:  func(m *Model) string { return m.exclusiveOverlayView()() },
			view:   func(m *Model) string { return m.mcpOverlay.View() },
			poison: func(m *Model) { m.overlayMemos.mcp.out = memoSentinel },
			mutations: append([]memoMutation{
				keyMutation("down", benchKeyDown, true),
				keyMutation("pgdn", benchKeyPgDn, true),
				keyMutation("up", benchKeyUp, true),
			}, commonMemoMutations(true, false)...),
		},
		{
			name: "lsp",
			open: func(_ *testing.T, m *Model) {
				m.lspServers, m.lspEnabled = benchLSPServers(), true
				m.executeShowLSPAction()
			},
			frame:  func(m *Model) string { return m.exclusiveOverlayView()() },
			view:   func(m *Model) string { return m.lspOverlay.View() },
			poison: func(m *Model) { m.overlayMemos.lsp.out = memoSentinel },
			mutations: append([]memoMutation{
				keyMutation("down", benchKeyDown, false),
			}, commonMemoMutations(true, false)...),
		},
		{
			name: "context",
			open: func(_ *testing.T, m *Model) {
				m.contextOverlay = openContextOverlay("Context", benchContextReport(), m.width, m.height, m.styles, m.content.glamourStyleSheet)
			},
			frame:  func(m *Model) string { return m.renderContextOverlay() },
			view:   func(m *Model) string { return m.buildContextOverlay(&contextStyledLines{}) },
			poison: func(m *Model) { poisonContextMemo(m, 0, 0) },
			mutations: append([]memoMutation{
				keyMutation("down", benchKeyDown, true),
				keyMutation("pgdn", benchKeyPgDn, true),
				keyMutation("up", benchKeyUp, true),
			}, commonMemoMutations(true, true)...),
		},
	}
}

func newMemoTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{Model: "memo-model", ModelContexts: map[string]int{"memo-model": 4096}}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 160, Height: 50})
	m.syncViewport()
	return m
}

func TestOverlayRenderMemo(t *testing.T) {
	t.Parallel()
	for _, tc := range memoCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newMemoTestModel(t)
			tc.open(t, m)

			first := tc.frame(m)
			if first == "" {
				t.Fatal("overlay rendered empty")
			}
			if want := tc.view(m); first != want {
				t.Fatal("memoised render differs from the uncached render")
			}
			if got := tc.frame(m); got != first {
				t.Fatal("repeat render with unchanged state differs")
			}
			tc.poison(m)
			if got := tc.frame(m); got != memoSentinel {
				t.Fatal("unchanged state re-rendered instead of reusing the memo")
			}

			m.overlayMemos = overlayRenderMemos{}
			prev := first
			for _, mut := range tc.mutations {
				m = mut.apply(t, m)
				got := tc.frame(m)
				if want := tc.view(m); got != want {
					t.Errorf("after %s: memoised render differs from the uncached render", mut.name)
				}
				if mut.changes && got == prev {
					t.Errorf("after %s: render unchanged; the mutation should alter the overlay", mut.name)
				}
				if got == memoSentinel {
					t.Errorf("after %s: stale memo served after a state change", mut.name)
				}
				prev = got
			}

			framed := m.View().Content
			m.overlayMemos = overlayRenderMemos{}
			m.overlayCache = overlayComposeCache{}
			if again := m.View().Content; again != framed {
				t.Error("full frame from warm memos differs from the frame with cold memos")
			}
		})
	}
}

func TestHelpKeepsViewportCacheAndMemoisesPanel(t *testing.T) {
	t.Parallel()
	m := newMemoTestModel(t)
	for i := 0; i < 80; i++ {
		m.content.AppendLine("conversation line")
	}
	m.syncViewport()
	cw := m.contentWidth()

	plain := m.renderViewportView(cw)
	m.helpVisible = true
	got := m.renderViewportView(cw)
	helpWidth := max(20, cw-4)
	want := composeCenteredOverlay(plain, renderHelp(m.styles, helpWidth), cw, lipgloss.Height(plain))
	if got != want {
		t.Fatal("help composition differs from the uncached composition")
	}
	if m.vpViewCache != plain {
		t.Fatal("viewport cache not populated with the plain view while help is visible")
	}

	m.vpViewCache = strings.Repeat("poisoned\n", lipgloss.Height(plain)-1) + "poisoned"
	if out := m.renderViewportView(cw); !strings.Contains(out, "poisoned") {
		t.Error("viewport cache not reused while help is visible")
	}

	m.overlayMemos.help.out = memoSentinel
	if m.renderHelpMemo(helpWidth) != memoSentinel {
		t.Error("help panel re-rendered for unchanged styles and width")
	}
	if m.renderHelpMemo(helpWidth+1) == memoSentinel {
		t.Error("help panel reused across widths")
	}
}

func TestContextOverlayBoundsMatchUncachedMeasurement(t *testing.T) {
	t.Parallel()
	oracle := func(m *Model) (x, y, w, h int) {
		lines := strings.Split(m.buildContextOverlay(&contextStyledLines{}), "\n")
		h = len(lines)
		for _, line := range lines {
			w = max(w, lipgloss.Width(line))
		}
		startX, startY := (m.width-w)/2, (m.height-h)/2
		endX, endY := min(m.width, startX+w), min(m.height, startY+h)
		x, y = max(0, startX), max(0, startY)
		w, h = endX-x, endY-y
		if w <= 0 || h <= 0 {
			return 0, 0, 0, 0
		}
		return x, y, w, h
	}
	for _, size := range raceSample([][2]int{{80, 24}, {300, 80}, {24, 8}}) {
		for _, items := range raceSample([]int{31, 0, 80}) {
			m := newMemoTestModel(t)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.contextOverlay = openContextOverlay("Context", contextOverlayMouseReport(items), m.width, m.height, m.styles, m.content.glamourStyleSheet)
			maxOffset := max(0, m.contextOverlay.lineCount-contextOverlayMaxLines)
			for _, offset := range []int{0, maxOffset / 2, maxOffset} {
				m.contextOverlay.scrollOffset = offset
				wx, wy, ww, wh := oracle(m)
				gx, gy, gw, gh := m.contextOverlayBounds()
				if [4]int{gx, gy, gw, gh} != [4]int{wx, wy, ww, wh} {
					t.Errorf("size %v items %d offset %d: bounds = %v, want %v", size, items, offset, [4]int{gx, gy, gw, gh}, [4]int{wx, wy, ww, wh})
				}
				if got, want := m.renderContextOverlay(), m.buildContextOverlay(&contextStyledLines{}); got != want {
					t.Errorf("size %v items %d offset %d: memoised render differs from a fresh render", size, items, offset)
				}
			}
		}
	}
}

func TestContextOverlayBoundsDoNotRerender(t *testing.T) {
	t.Parallel()
	m := newMemoTestModel(t)
	m.contextOverlay = openContextOverlay("Context", contextOverlayMouseReport(40), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	_ = m.View()
	poisonContextMemo(m, 7, 3)
	_, _, w, h := m.contextOverlayBounds()
	if w != 7 || h != 3 {
		t.Errorf("bounds = %dx%d, want the memoised 7x3: bounds re-rendered the overlay", w, h)
	}
}

func TestCacheStatsOverlayBoundsReuseRender(t *testing.T) {
	t.Parallel()
	m := newMemoTestModel(t)
	m.executeOpenCacheStatsAction()
	if !m.contextOverlay.IsOpen() {
		t.Fatal("cache stats overlay not open")
	}
	_, _, w, h := m.contextOverlayBounds()
	if w == 0 || h == 0 {
		t.Fatalf("bounds = %dx%d, want a non-empty rectangle", w, h)
	}
	poisonContextMemo(m, 0, 0)
	if m.renderContextOverlay() != memoSentinel {
		t.Error("cache stats overlay re-rendered with unchanged state")
	}
}

func poisonContextMemo(m *Model, w, h int) {
	for i := range m.overlayMemos.context.entries {
		e := &m.overlayMemos.context.entries[i]
		e.out = memoSentinel
		if w != 0 {
			e.w, e.h = w, h
		}
	}
}

func TestContextOverlayMemoKeepsRecentScrollPositions(t *testing.T) {
	t.Parallel()
	m := newMemoTestModel(t)
	m.contextOverlay = openContextOverlay("Context", benchContextReport(), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	render := func(offset int) string {
		m.contextOverlay.scrollOffset = offset
		return m.renderContextOverlay()
	}
	top, next := render(0), render(1)
	if top == next {
		t.Fatal("scrolling by one line did not change the overlay")
	}
	poisonContextMemo(m, 0, 0)
	if render(0) != memoSentinel || render(1) != memoSentinel {
		t.Error("recent scroll positions were re-rendered")
	}
	for offset := 2; offset < 2+contextMemoSlots; offset++ {
		render(offset)
	}
	if got := render(0); got == memoSentinel || got != top {
		t.Error("evicted scroll position not re-rendered to the uncached output")
	}
}
