package theme

import (
	"image/color"
	"testing"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

type mockTheme struct{ id string }

func (m mockTheme) Background() color.Color                       { return lipgloss.Color("#000") }
func (m mockTheme) Foreground() color.Color                       { return lipgloss.Color("#fff") }
func (m mockTheme) Accent() color.Color                           { return lipgloss.Color("#f00") }
func (m mockTheme) Muted() color.Color                            { return lipgloss.Color("#888") }
func (m mockTheme) Border() color.Color                           { return lipgloss.Color("#555") }
func (m mockTheme) Error() color.Color                            { return lipgloss.Color("#f00") }
func (m mockTheme) Warning() color.Color                          { return lipgloss.Color("#ff0") }
func (m mockTheme) Success() color.Color                          { return lipgloss.Color("#0f0") }
func (m mockTheme) SyntaxKeyword() color.Color                    { return lipgloss.Color("#f00") }
func (m mockTheme) SyntaxString() color.Color                     { return lipgloss.Color("#0f0") }
func (m mockTheme) SyntaxComment() color.Color                    { return lipgloss.Color("#888") }
func (m mockTheme) SyntaxFunction() color.Color                   { return lipgloss.Color("#00f") }
func (m mockTheme) SyntaxNumber() color.Color                     { return lipgloss.Color("#ff0") }
func (m mockTheme) SyntaxOperator() color.Color                   { return lipgloss.Color("#f0f") }
func (m mockTheme) LipGlossStyles() Styles                        { return Styles{} }
func (m mockTheme) GlamourStyleSheet() glamour.TermRendererOption { return nil }

func TestRegisterAndGet(t *testing.T) {
	Register("test-dark", mockTheme{id: "dark"})
	got, err := Get("test-dark")
	if err != nil {
		t.Fatalf("Get(test-dark) = _, %v", err)
	}
	if _, ok := got.(mockTheme); !ok {
		t.Fatalf("Get(test-dark) returned %T, want mockTheme", got)
	}
}

func TestGetFallback(t *testing.T) {
	got, err := Get("nonexistent-theme")
	if err != nil {
		t.Fatalf("Get(nonexistent) = _, %v; want fallback to steiner", err)
	}
	if _, ok := got.(steinerTheme); !ok {
		t.Fatalf("Get(nonexistent) returned %T, want steinerTheme fallback", got)
	}
}

func TestDefault(t *testing.T) {
	// steiner is registered in init(), so it should be the default
	d := Default()
	if d == nil {
		t.Fatal("Default() returned nil, expected steiner theme")
	}
	if _, ok := d.(steinerTheme); !ok {
		t.Fatalf("Default() returned %T, want steinerTheme", d)
	}
}

func withIsolatedRegistry(t *testing.T, fn func(t *testing.T)) {
	mu.Lock()
	savedThemes := themes
	savedDefName := defName
	themes = make(map[string]Theme)
	defName = ""
	mu.Unlock()

	defer func() {
		mu.Lock()
		themes = savedThemes
		defName = savedDefName
		mu.Unlock()
	}()

	fn(t)
}

func TestGetFallsBackAndErrors(t *testing.T) {
	withIsolatedRegistry(t, func(t *testing.T) {
		Register("test-theme", mockTheme{id: "test"})

		got, err := Get("test-theme")
		if err != nil {
			t.Fatalf("Get(registered theme) should not error, got %v", err)
		}
		if _, ok := got.(mockTheme); !ok {
			t.Fatalf("Get(test-theme) returned %T, want mockTheme", got)
		}

		_, err = Get("nonexistent")
		if err == nil {
			t.Fatal("Get(nonexistent) should error when steiner is not available, got nil")
		}
	})
}

func TestGetWithEmptyRegistry(t *testing.T) {
	withIsolatedRegistry(t, func(t *testing.T) {
		_, err := Get("any-name")
		if err == nil {
			t.Fatal("Get on empty registry should error, got nil")
		}
	})
}

func TestDefaultWithIsolatedRegistry(t *testing.T) {
	withIsolatedRegistry(t, func(t *testing.T) {
		d := Default()
		if d != nil {
			t.Fatalf("Default on empty registry should return nil, got %v", d)
		}
	})

	withIsolatedRegistry(t, func(t *testing.T) {
		Register("only-theme", mockTheme{id: "only"})
		d := Default()
		if d == nil {
			t.Fatal("Default with one registered theme should return it, got nil")
		}
		if _, ok := d.(mockTheme); !ok {
			t.Fatalf("Default returned %T, want mockTheme", d)
		}
	})
}
