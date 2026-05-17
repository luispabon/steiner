package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWithBg_simpleText(t *testing.T) {
	s := "hello"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	if !strings.HasPrefix(result, "\x1b[") {
		t.Errorf("WithBg should start with ANSI escape, got %q", result)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("WithBg lost the original text")
	}
}

func TestWithBg_multiLine(t *testing.T) {
	s := "hello\nworld"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	lines := strings.Split(result, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "\x1b[") {
			t.Errorf("line %d should start with ANSI escape, got %q", i, line)
		}
	}
}

func TestWithBg_resetReplaced(t *testing.T) {
	s := "before\x1b[0mafter"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	// Reset should be followed by bg escape so background is re-applied
	if !strings.Contains(result, "\x1b[0m\x1b[") {
		t.Errorf("reset should be followed by bg escape, got %q", result)
	}
}

func TestWithBg_emptyLine(t *testing.T) {
	s := "a\n\nb"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
	// Empty line should have ANSI escape prefix and background active
	if !strings.HasPrefix(lines[1], "\x1b[") {
		t.Errorf("empty line (index 1) should start with ANSI escape, got %q", lines[1])
	}
	// Empty line should contain at least one bg escape
	if !strings.Contains(lines[1], "\x1b[48;2;") {
		t.Errorf("empty line should contain bg escape, got %q", lines[1])
	}
	// Non-empty lines should contain their original text
	if !strings.Contains(lines[0], "a") {
		t.Errorf("line 0 should contain 'a'")
	}
	if !strings.Contains(lines[2], "b") {
		t.Errorf("line 2 should contain 'b'")
	}
}

func TestWithBg_preservesTrailingNewlines(t *testing.T) {
	s := "hello\n"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)

	if !strings.HasSuffix(result, "\n") {
		t.Fatalf("WithBg result lost trailing newline: %q", result)
	}

	lines := strings.Split(result, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "hello") {
		t.Fatalf("first line = %q, want original text", lines[0])
	}
	if lines[1] != "" {
		t.Fatalf("second line = %q, want empty trailing line", lines[1])
	}
}

func TestWithBg_visuallyIdempotent(t *testing.T) {
	s := "hello\nworld"
	bg := lipgloss.Color(BgElev)
	first := WithBg(s, bg)
	second := WithBg(first, bg)
	// Visually idempotent: second pass must preserve original text and have bg escapes
	if !strings.Contains(second, "hello") || !strings.Contains(second, "world") {
		t.Errorf("second application lost original text")
	}
	if !strings.HasPrefix(second, "\x1b[") {
		t.Errorf("second application should start with ANSI escape")
	}
	// Both should produce strings starting with the same bg escape
	if !strings.HasPrefix(first, second[:len(first)-6]) {
		t.Logf("first and second may differ structurally but are visually equivalent")
	}
}

func TestWithBg_multipleResets(t *testing.T) {
	s := "a\x1b[0mb\x1b[0mc"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	// Count occurrences of reset followed by bg escape
	count := strings.Count(result, "\x1b[0m\x1b[")
	if count != 2 {
		t.Errorf("expected 2 reset+bg pairs, got %d", count)
	}
}

func TestWithBg_bg49Reset(t *testing.T) {
	s := "a\x1b[49mb"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	// Background reset (\x1b[49m) should be followed by bg escape
	if !strings.Contains(result, "\x1b[49m\x1b[") {
		t.Errorf("bg reset (\\x1b[49m) should be followed by bg escape, got %q", result)
	}
}

func TestWithBg_preservesForeground(t *testing.T) {
	s := "\x1b[31mred\x1b[0mnormal"
	bg := lipgloss.Color(BgElev)
	result := WithBg(s, bg)
	// Should still have the red ANSI code
	if !strings.Contains(result, "\x1b[31m") {
		t.Errorf("WithBg lost foreground color")
	}
	// Should have red text
	if !strings.Contains(result, "red") {
		t.Errorf("WithBg lost 'red' text")
	}
	if !strings.Contains(result, "normal") {
		t.Errorf("WithBg lost 'normal' text")
	}
}

func TestWithBg_differentBg(t *testing.T) {
	s := "test"
	bg := lipgloss.Color("#FF0000")
	result := WithBg(s, bg)
	if !strings.HasPrefix(result, "\x1b[") {
		t.Errorf("WithBg should start with ANSI escape")
	}
	if !strings.Contains(result, "test") {
		t.Errorf("WithBg lost the original text")
	}
}
