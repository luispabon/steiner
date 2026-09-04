package theme

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"unsafe"

	"charm.land/lipgloss/v2"
)

func styleSnapshot(style lipgloss.Style) string {
	top, right, bottom, left := style.GetPadding()
	return fmt.Sprintf(
		"fg=%s bg=%s bold=%t italic=%t padding=%d,%d,%d,%d",
		terminalColorSnapshot(style.GetForeground()),
		terminalColorSnapshot(style.GetBackground()),
		style.GetBold(),
		style.GetItalic(),
		top, right, bottom, left,
	)
}

func terminalColorSnapshot(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, a := c.RGBA()
	if r == 0 && g == 0 && b == 0 && a == 0 {
		return ""
	}
	if snapshot := fmt.Sprint(c); snapshot == "{}" {
		return ""
	}
	// Reconstruct hex from RGBA (values are in [0, 0xffff] range).
	return strings.ToLower(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
}

func styleFromMap(styles map[string]lipgloss.Style, key string, fallback lipgloss.Style) lipgloss.Style {
	if style, ok := styles[key]; ok {
		return style
	}
	return fallback
}

func TestWithBg_simpleText(t *testing.T) {
	s := "hello"
	bg := BgElev
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
	bg := BgElev
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
	bg := BgElev
	result := WithBg(s, bg)
	if !strings.Contains(result, "\x1b[0m\x1b[") {
		t.Errorf("reset should be followed by bg escape, got %q", result)
	}
}

func TestWithBg_shortResetReplaced(t *testing.T) {
	s := "before\x1b[mafter"
	bg := BgElev
	result := WithBg(s, bg)
	if !strings.Contains(result, "\x1b[m\x1b[") {
		t.Errorf("short reset should be followed by bg escape, got %q", result)
	}
}

func TestWithBg_emptyLine(t *testing.T) {
	s := "a\n\nb"
	bg := BgElev
	result := WithBg(s, bg)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[1], "\x1b[") {
		t.Errorf("empty line (index 1) should start with ANSI escape, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "\x1b[48;2;") {
		t.Errorf("empty line should contain bg escape, got %q", lines[1])
	}
	if !strings.Contains(lines[0], "a") {
		t.Errorf("line 0 should contain 'a'")
	}
	if !strings.Contains(lines[2], "b") {
		t.Errorf("line 2 should contain 'b'")
	}
}

func TestWithBg_preservesTrailingNewlines(t *testing.T) {
	s := "hello\n"
	bg := BgElev
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

// stripANSI removes every CSI escape sequence from s, leaving the visible
// text and newline structure.
func stripANSI(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// TestWithBg_visuallyIdempotent pins the idempotence contract stated in the
// WithBg doc comment: re-applying the same background may legitimately double
// up background escape sequences, but the visible text and newline structure
// must be identical between one and two applications, and every line must
// still carry the background.
func TestWithBg_visuallyIdempotent(t *testing.T) {
	bg := BgElev
	bgSeq := "\x1b[48;2;"

	tests := []struct {
		name  string
		input string
	}{
		{name: "single line", input: "hello"},
		{name: "multi line", input: "hello\nworld"},
		{name: "empty line", input: "a\n\nb"},
		{name: "trailing newline", input: "hello\n"},
		{name: "embedded long reset", input: "before\x1b[0mafter"},
		{name: "embedded short reset", input: "before\x1b[mafter"},
		{name: "embedded bg reset", input: "a\x1b[49mb"},
		{name: "embedded foreground", input: "\x1b[31mred\x1b[0mnormal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := WithBg(tt.input, bg)
			second := WithBg(first, bg)

			if got, want := stripANSI(second), stripANSI(first); got != want {
				t.Fatalf("second application changed visible text: got %q, want %q", got, want)
			}
			for i, line := range strings.Split(strings.TrimSuffix(second, "\n"), "\n") {
				if !strings.HasPrefix(line, bgSeq) {
					t.Fatalf("line %d of second application = %q, want background escape prefix", i, line)
				}
			}
		})
	}
}

func TestWithBg_multipleResets(t *testing.T) {
	s := "a\x1b[0mb\x1b[0mc"
	bg := BgElev
	result := WithBg(s, bg)
	count := strings.Count(result, "\x1b[0m\x1b[")
	if count != 2 {
		t.Errorf("expected 2 reset+bg pairs, got %d", count)
	}
}

func TestWithBg_multipleShortResets(t *testing.T) {
	s := "a\x1b[mb\x1b[mc"
	bg := BgElev
	result := WithBg(s, bg)
	count := strings.Count(result, "\x1b[m\x1b[")
	if count != 2 {
		t.Errorf("expected 2 short reset+bg pairs, got %d", count)
	}
}

func TestWithBg_bg49Reset(t *testing.T) {
	s := "a\x1b[49mb"
	bg := BgElev
	result := WithBg(s, bg)
	if !strings.Contains(result, "\x1b[49m\x1b[") {
		t.Errorf("bg reset (\\x1b[49m) should be followed by bg escape, got %q", result)
	}
}

func TestWithBg_preservesForeground(t *testing.T) {
	s := "\x1b[31mred\x1b[0mnormal"
	bg := BgElev
	result := WithBg(s, bg)
	if !strings.Contains(result, "\x1b[31m") {
		t.Errorf("WithBg lost foreground color")
	}
	if !strings.Contains(result, "red") {
		t.Errorf("WithBg lost 'red' text")
	}
	if !strings.Contains(result, "normal") {
		t.Errorf("WithBg lost 'normal' text")
	}
}

func TestWithBg_differentBg(t *testing.T) {
	s := "test"
	bg := "#FF0000"
	result := WithBg(s, bg)
	if !strings.HasPrefix(result, "\x1b[") {
		t.Errorf("WithBg should start with ANSI escape")
	}
	if !strings.Contains(result, "test") {
		t.Errorf("WithBg lost the original text")
	}
}

func TestBuildStylesToolStyleSnapshots(t *testing.T) {
	styles := BuildStyles(AccentAmber)

	tests := []struct {
		name string
		got  lipgloss.Style
		want string
	}{
		{name: "tool bash", got: styleFromMap(styles.ToolTagStyles, "bash", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(AccentAmber) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool read", got: styleFromMap(styles.ToolTagStyles, "read", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolCyan) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool read_file", got: styleFromMap(styles.ToolTagStyles, "read_file", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolCyan) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool mutate", got: styleFromMap(styles.ToolTagStyles, "mutate", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolGrn) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool grep", got: styleFromMap(styles.ToolTagStyles, "grep", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolMag) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool search", got: styleFromMap(styles.ToolTagStyles, "search", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolBlue) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool glob", got: styleFromMap(styles.ToolTagStyles, "glob", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolBlue) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool fetch_url", got: styleFromMap(styles.ToolTagStyles, "fetch_url", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolBlue) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool todo", got: styleFromMap(styles.ToolTagStyles, "todo", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(Warn) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool default", got: styleFromMap(styles.ToolTagStyles, "ls", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolBlue) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool fallback", got: styleFromMap(styles.ToolTagStyles, "unknown", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolBlue) + " bold=true italic=false padding=0,1,0,1"},
		{name: "tool border bash", got: styleFromMap(styles.ToolBorderStyles, "bash", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolAmberLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border read", got: styleFromMap(styles.ToolBorderStyles, "read", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolCyanLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border read_file", got: styleFromMap(styles.ToolBorderStyles, "read_file", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolCyanLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border mutate", got: styleFromMap(styles.ToolBorderStyles, "mutate", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolGrnLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border grep", got: styleFromMap(styles.ToolBorderStyles, "grep", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolMagLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border search", got: styleFromMap(styles.ToolBorderStyles, "search", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolBlueLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border glob", got: styleFromMap(styles.ToolBorderStyles, "glob", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolBlueLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border fetch_url", got: styleFromMap(styles.ToolBorderStyles, "fetch_url", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolBlueLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border todo", got: styleFromMap(styles.ToolBorderStyles, "todo", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(WarnLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border fallback", got: styleFromMap(styles.ToolBorderStyles, "unknown", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(AccentLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate explore", got: styleFromMap(styles.DelegateTagStyles, "explore", styles.ToolTagDefault), want: "fg=" + strings.ToLower(ToolCyan) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate research", got: styleFromMap(styles.DelegateTagStyles, "research", styles.ToolTagDefault), want: "fg=" + strings.ToLower(DelegateViolet) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate code", got: styleFromMap(styles.DelegateTagStyles, "code", styles.ToolTagDefault), want: "fg=" + strings.ToLower(AccentAmber) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate evaluate", got: styleFromMap(styles.DelegateTagStyles, "evaluate", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Thinking) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate sanity_check", got: styleFromMap(styles.DelegateTagStyles, "sanity_check", styles.ToolTagDefault), want: "fg=" + strings.ToLower(ToolMag) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate review", got: styleFromMap(styles.DelegateTagStyles, "review", styles.ToolTagDefault), want: "fg=" + strings.ToLower(DelegateReview) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate vision", got: styleFromMap(styles.DelegateTagStyles, "vision", styles.ToolTagDefault), want: "fg=" + strings.ToLower(ToolBlue) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate advisor", got: styleFromMap(styles.DelegateTagStyles, "advisor", styles.ToolTagDefault), want: "fg=" + strings.ToLower(AdvisorGreen) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate fallback", got: styleFromMap(styles.DelegateTagStyles, "delegate", styles.ToolTagDefault), want: "fg=" + strings.ToLower(Black) + " bg=" + strings.ToLower(ToolBlue) + " bold=true italic=false padding=0,1,0,1"},
		{name: "delegate border explore", got: styleFromMap(styles.DelegateBorderStyles, "explore", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolCyanLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border research", got: styleFromMap(styles.DelegateBorderStyles, "research", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(DelegateVioletLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border code", got: styleFromMap(styles.DelegateBorderStyles, "code", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolAmberLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border evaluate", got: styleFromMap(styles.DelegateBorderStyles, "evaluate", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(DelegateThinkingLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border sanity_check", got: styleFromMap(styles.DelegateBorderStyles, "sanity_check", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolMagLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border review", got: styleFromMap(styles.DelegateBorderStyles, "review", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(DelegateReviewLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border vision", got: styleFromMap(styles.DelegateBorderStyles, "vision", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolBlueLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border advisor", got: styleFromMap(styles.DelegateBorderStyles, "advisor", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(AdvisorGreenLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border fallback", got: styleFromMap(styles.DelegateBorderStyles, "delegate", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(AccentLine) + " bg= bold=false italic=false padding=0,0,0,0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := styleSnapshot(tt.got); got != tt.want {
				t.Fatalf("style snapshot = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestColorHex(t *testing.T) {
	tests := []struct {
		name string
		c    color.Color
		want string
	}{
		{name: "nil color", c: nil, want: ""},
		{name: "black opaque", c: lipgloss.Color("#000000"), want: "#000000"},
		{name: "white opaque", c: lipgloss.Color("#FFFFFF"), want: "#ffffff"},
		{name: "red opaque", c: lipgloss.Color("#FF0000"), want: "#ff0000"},
		{name: "custom hex", c: lipgloss.Color("#E8814B"), want: "#e8814b"},
		{name: "zero rgba", c: &color.RGBA{R: 0, G: 0, B: 0, A: 0}, want: "#000000"},
		{name: "opaque rgba", c: &color.RGBA{R: 255, G: 0, B: 0, A: 255}, want: "#ff0000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ColorHex(tt.c)
			if got != tt.want {
				t.Errorf("ColorHex() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBgEscapeMalformed(t *testing.T) {
	tests := []struct {
		name string
		bg   string
		want string
	}{
		{name: "empty string", bg: "", want: ""},
		{name: "hash only", bg: "#", want: ""},
		{name: "short hex", bg: "#fff", want: ""},
		{name: "long hex", bg: "#aabbccdd", want: ""},
		{name: "no hash prefix works", bg: "aabbcc", want: "\x1b[48;2;170;187;204m"},
		{name: "non-hex chars returns black", bg: "#gggggg", want: "\x1b[48;2;0;0;0m"},
		{name: "valid hex", bg: "#aabbcc", want: "\x1b[48;2;170;187;204m"},
		{name: "valid hex uppercase", bg: "#AABBCC", want: "\x1b[48;2;170;187;204m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bgEscape(tt.bg)
			if got != tt.want {
				t.Errorf("bgEscape(%q) = %q, want %q", tt.bg, got, tt.want)
			}
		})
	}
}

func TestWithBgEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		bg    string
		check func(t *testing.T, got string)
	}{
		{
			name: "empty input",
			s:    "",
			bg:   BgElev,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("empty input should produce empty output, got %q", got)
				}
			},
		},
		{
			name: "only newlines",
			s:    "\n\n",
			bg:   BgElev,
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "\n\n") {
					t.Errorf("only newlines input should preserve newlines, got %q", got)
				}
			},
		},
		{
			name: "line with mixed resets",
			s:    "a\x1b[mtext\x1b[0mb",
			bg:   BgElev,
			check: func(t *testing.T, got string) {
				if stripANSI(got) != "atextb" {
					t.Errorf("mixed resets: stripped text = %q, want %q", stripANSI(got), "atextb")
				}
			},
		},
		{
			name: "line already ending in bg reset",
			s:    "test\x1b[49m",
			bg:   BgElev,
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "test") {
					t.Errorf("bg reset suffix: lost text")
				}
			},
		},
		{
			name: "invalid bg hex passthrough",
			s:    "hello",
			bg:   "#invalid",
			check: func(t *testing.T, got string) {
				if got != "hello" {
					t.Errorf("invalid bg hex should passthrough unchanged, got %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithBg(tt.s, tt.bg)
			tt.check(t, got)
		})
	}
}

// TestTruncateAndPadVertical pins TruncateAndPadVertical's byte-level contract:
// truncation at the maxHeight-th newline, exact-fit and over-height early
// returns that must return the input string itself (no copy — verified via
// unsafe.StringData), and padding with exactly maxHeight lines using the same
// lipgloss background render the function uses.
func TestTruncateAndPadVertical(t *testing.T) {
	padLine := func(width int, bg string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render(strings.Repeat(" ", width))
	}

	tests := []struct {
		name      string
		s         string
		width     int
		maxHeight int
		bg        string
		want      string
		noCopy    bool // early return must return s itself, not a copy
	}{
		{
			name:      "single line no trailing newline early return",
			s:         "hello",
			width:     10,
			maxHeight: 1,
			bg:        "#123456",
			want:      "hello",
			noCopy:    true,
		},
		{
			name:      "short content padded one line",
			s:         "hello",
			width:     5,
			maxHeight: 2,
			bg:        "#123456",
			want:      "hello" + "\n" + padLine(5, "#123456"),
		},
		{
			name:      "short content padded two lines",
			s:         "hello",
			width:     5,
			maxHeight: 3,
			bg:        "#123456",
			want:      "hello" + "\n" + padLine(5, "#123456") + "\n" + padLine(5, "#123456"),
		},
		{
			name:      "exact fit early return",
			s:         "l1\nl2",
			width:     5,
			maxHeight: 2,
			bg:        "#123456",
			want:      "l1\nl2",
			noCopy:    true,
		},
		{
			name:      "truncate at maxHeight-th newline",
			s:         "l1\nl2\nl3",
			width:     5,
			maxHeight: 2,
			bg:        "#123456",
			want:      "l1\nl2",
		},
		{
			name:      "truncate with trailing newline",
			s:         "l1\nl2\n",
			width:     5,
			maxHeight: 1,
			bg:        "#123456",
			want:      "l1",
		},
		{
			name:      "utf8 content containing newline",
			s:         "héllo\nwörld",
			width:     5,
			maxHeight: 1,
			bg:        "#123456",
			want:      "héllo",
		},
		{
			name:      "maxHeight zero truncates at first newline",
			s:         "l1\nl2",
			width:     5,
			maxHeight: 0,
			bg:        "#123456",
			want:      "l1",
		},
		{
			name:      "maxHeight zero without newline returns input",
			s:         "l1",
			width:     5,
			maxHeight: 0,
			bg:        "#123456",
			want:      "l1",
		},
		{
			name:      "empty string padded",
			s:         "",
			width:     5,
			maxHeight: 2,
			bg:        "#123456",
			want:      "\n" + padLine(5, "#123456"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateAndPadVertical(tt.s, tt.width, tt.maxHeight, tt.bg)
			if got != tt.want {
				t.Fatalf("TruncateAndPadVertical(%q, %d, %d, %q) = %q, want %q", tt.s, tt.width, tt.maxHeight, tt.bg, got, tt.want)
			}
			if tt.noCopy && unsafe.StringData(got) != unsafe.StringData(tt.s) {
				t.Fatalf("early return copied the input: StringData(got) %p != StringData(s) %p", unsafe.StringData(got), unsafe.StringData(tt.s))
			}
		})
	}
}
