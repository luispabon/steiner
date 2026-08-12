package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestHandleSetAccentMsgRepointsAllComponents ensures every sub-component
// holding a *theme.Styles field is re-pointed at the rebuilt Model.styles
// after a setAccentMsg. A component left stale keeps rendering with the
// previous accent colour until the app restarts.
func TestHandleSetAccentMsgRepointsAllComponents(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:         "test-model",
		ModelContexts: map[string]int{"test-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	oldStyles := m.styles

	m = updateModelDirect(m, setAccentMsg{preset: "violet"})

	if m.styles == oldStyles {
		t.Fatal("expected m.styles to be a new pointer after accent change")
	}

	cases := []struct {
		name   string
		styles *theme.Styles
	}{
		{"content", m.content.styles},
		{"sidebar", m.sidebar.styles},
		{"status", m.status.styles},
		{"slashOverlay", m.slashOverlay.styles},
		{"fileList", m.fileList.styles},
		{"mcpOverlay", m.mcpOverlay.styles},
		{"filePicker", m.filePicker.styles},
		{"sessionPicker", m.sessionPicker.styles},
		{"modelPicker", m.modelPicker.styles},
		{"reasoningPicker", m.reasoningPicker.styles},
		{"planPicker", m.planPicker.styles},
		{"accentPicker", m.accentPicker.styles},
		{"oneshotResumePicker", m.oneshotResumePicker.styles},
	}

	for _, tc := range cases {
		if tc.styles != m.styles {
			t.Errorf("%s.styles is stale: expected %p, got %p", tc.name, m.styles, tc.styles)
		}
	}
}

// TestHandleSetAccentMsgRepointsEveryStylesField is the future-proof companion
// to the table above: it reflects over Model's fields rather than naming them,
// so a sub-component added later is covered without anyone remembering to
// extend the list. reflect.Value.Pointer works on unexported fields, unlike
// Interface, which is why the comparison is by address.
func TestHandleSetAccentMsgRepointsEveryStylesField(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:         "test-model",
		ModelContexts: map[string]int{"test-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModelDirect(m, setAccentMsg{preset: "violet"})

	stylesPtrType := reflect.TypeOf((*theme.Styles)(nil))
	want := reflect.ValueOf(m.styles).Pointer()

	// contextOverlay is deliberately excluded: it is rebuilt by
	// openContextOverlay with the current styles every time it opens, so it
	// holds a stale pointer while closed by design.
	exempt := map[string]string{"contextOverlay": "rebuilt on open"}

	v := reflect.ValueOf(m)
	checked := 0
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		field := v.Field(i)
		if field.Kind() != reflect.Struct {
			continue
		}
		sub := field.FieldByName("styles")
		if !sub.IsValid() || sub.Type() != stylesPtrType {
			continue
		}
		if _, ok := exempt[name]; ok {
			continue
		}
		checked++
		if sub.Pointer() != want {
			t.Errorf("%s.styles is stale after accent change: expected %#x, got %#x; "+
				"add it to the re-point list in handleSetAccentMsg", name, want, sub.Pointer())
		}
	}

	// Positive control: if the reflection ever stops finding fields, the loop
	// above would pass while asserting nothing.
	if checked == 0 {
		t.Fatal("found no styles fields on Model; the reflective scan asserts nothing")
	}
}
