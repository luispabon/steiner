package theme

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

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
		{name: "tool border todo", got: styleFromMap(styles.ToolBorderStyles, "todo", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(WarnLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "tool border fallback", got: styleFromMap(styles.ToolBorderStyles, "unknown", styles.ToolBorderDefault), want: "fg=" + strings.ToLower(ToolBlueLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate explore", got: styleFromMap(styles.DelegateTagStyles, "explore", styles.DelegateTagDefault), want: "fg=" + strings.ToLower(ToolCyan) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate research", got: styleFromMap(styles.DelegateTagStyles, "research", styles.DelegateTagDefault), want: "fg=" + strings.ToLower(DelegateViolet) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate code", got: styleFromMap(styles.DelegateTagStyles, "code", styles.DelegateTagDefault), want: "fg=" + strings.ToLower(AccentAmber) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate plan", got: styleFromMap(styles.DelegateTagStyles, "plan", styles.DelegateTagDefault), want: "fg=" + strings.ToLower(Thinking) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate verify", got: styleFromMap(styles.DelegateTagStyles, "verify", styles.DelegateTagDefault), want: "fg=" + strings.ToLower(ToolMag) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate fallback", got: styleFromMap(styles.DelegateTagStyles, "delegate", styles.DelegateTagDefault), want: "fg=" + strings.ToLower(ToolGrn) + " bg= bold=true italic=false padding=0,0,0,0"},
		{name: "delegate border explore", got: styleFromMap(styles.DelegateBorderStyles, "explore", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolCyanLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border research", got: styleFromMap(styles.DelegateBorderStyles, "research", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(DelegateVioletLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border code", got: styleFromMap(styles.DelegateBorderStyles, "code", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolAmberLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border plan", got: styleFromMap(styles.DelegateBorderStyles, "plan", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(DelegateThinkingLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border verify", got: styleFromMap(styles.DelegateBorderStyles, "verify", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolMagLine) + " bg= bold=false italic=false padding=0,0,0,0"},
		{name: "delegate border fallback", got: styleFromMap(styles.DelegateBorderStyles, "advisor", styles.DelegateBorderDefault), want: "fg=" + strings.ToLower(ToolGrnLine) + " bg= bold=false italic=false padding=0,0,0,0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := styleSnapshot(tt.got); got != tt.want {
				t.Fatalf("style snapshot = %q, want %q", got, tt.want)
			}
		})
	}
}
