package theme

import (
	"strings"
	"testing"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

func TestBuildGlamourStyleSheet_nonNil(t *testing.T) {
	opt := BuildGlamourStyleSheet("#E8814B")
	if opt == nil {
		t.Fatal("BuildGlamourStyleSheet returned nil")
	}
}

func TestBuildGlamourStyleSheet_rendersBold(t *testing.T) {
	opt := BuildGlamourStyleSheet("#E8814B")
	r, err := glamour.NewTermRenderer(
		opt,
		glamour.WithWordWrap(80),
	)
	if err != nil {
		t.Fatalf("creating renderer: %v", err)
	}

	out, err := r.Render("**bold text**")
	if err != nil {
		t.Fatalf("rendering bold: %v", err)
	}
	if !strings.Contains(out, "bold text") {
		t.Errorf("rendered output lost text: %q", out)
	}
}

func TestBuildGlamourStyleSheet_rendersHeading(t *testing.T) {
	opt := BuildGlamourStyleSheet("#E8814B")
	r, err := glamour.NewTermRenderer(
		opt,
		glamour.WithWordWrap(80),
	)
	if err != nil {
		t.Fatalf("creating renderer: %v", err)
	}

	out, err := r.Render("# Heading")
	if err != nil {
		t.Fatalf("rendering heading: %v", err)
	}
	if !strings.Contains(out, "Heading") {
		t.Errorf("rendered output lost text: %q", out)
	}
}

func TestSteinerThemeColors(t *testing.T) {
	theme := steinerTheme{}
	tests := []struct {
		name    string
		getter  func() interface{}
		wantHex string
	}{
		{name: "Background", getter: func() interface{} { return theme.Background() }, wantHex: ColorHex(lipgloss.Color(Bg))},
		{name: "Foreground", getter: func() interface{} { return theme.Foreground() }, wantHex: ColorHex(lipgloss.Color(Fg))},
		{name: "Accent", getter: func() interface{} { return theme.Accent() }, wantHex: ColorHex(lipgloss.Color(AccentAmber))},
		{name: "Muted", getter: func() interface{} { return theme.Muted() }, wantHex: ColorHex(lipgloss.Color(FgMute))},
		{name: "Border", getter: func() interface{} { return theme.Border() }, wantHex: ColorHex(lipgloss.Color(Border))},
		{name: "Error", getter: func() interface{} { return theme.Error() }, wantHex: ColorHex(lipgloss.Color(Removed))},
		{name: "Warning", getter: func() interface{} { return theme.Warning() }, wantHex: ColorHex(lipgloss.Color(Warn))},
		{name: "Success", getter: func() interface{} { return theme.Success() }, wantHex: ColorHex(lipgloss.Color(Added))},
		{name: "SyntaxKeyword", getter: func() interface{} { return theme.SyntaxKeyword() }, wantHex: ColorHex(lipgloss.Color(AccentAmber))},
		{name: "SyntaxString", getter: func() interface{} { return theme.SyntaxString() }, wantHex: ColorHex(lipgloss.Color(Added))},
		{name: "SyntaxComment", getter: func() interface{} { return theme.SyntaxComment() }, wantHex: ColorHex(lipgloss.Color(FgMute))},
		{name: "SyntaxFunction", getter: func() interface{} { return theme.SyntaxFunction() }, wantHex: ColorHex(lipgloss.Color(User))},
		{name: "SyntaxNumber", getter: func() interface{} { return theme.SyntaxNumber() }, wantHex: ColorHex(lipgloss.Color(Warn))},
		{name: "SyntaxOperator", getter: func() interface{} { return theme.SyntaxOperator() }, wantHex: ColorHex(lipgloss.Color(Tool))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ColorHex(tt.getter().(interface {
				RGBA() (uint32, uint32, uint32, uint32)
			}))
			if got != tt.wantHex {
				t.Errorf("ColorHex(%s()) = %q, want %q", tt.name, got, tt.wantHex)
			}
		})
	}
}

func TestSteinerGlamourStyleSheet(t *testing.T) {
	theme := steinerTheme{}
	opt := theme.GlamourStyleSheet()
	if opt == nil {
		t.Fatal("GlamourStyleSheet() returned nil")
	}

	r, err := glamour.NewTermRenderer(opt, glamour.WithWordWrap(80))
	if err != nil {
		t.Fatalf("NewTermRenderer with steiner theme: %v", err)
	}
	if r == nil {
		t.Fatal("NewTermRenderer returned nil")
	}
}

func TestSteinerThemeLipGlossStylesMatchesBuildStyles(t *testing.T) {
	build := BuildStyles(AccentAmber)
	steiner := steinerTheme{}.LipGlossStyles()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "tool tag default", got: styleSnapshot(steiner.ToolTagDefault), want: styleSnapshot(build.ToolTagDefault)},
		{name: "tool border default", got: styleSnapshot(steiner.ToolBorderDefault), want: styleSnapshot(build.ToolBorderDefault)},
		{name: "delegate border default", got: styleSnapshot(steiner.DelegateBorderDefault), want: styleSnapshot(build.DelegateBorderDefault)},
		{name: "tool tag grep", got: styleSnapshot(steiner.ToolTagStyles["grep"]), want: styleSnapshot(build.ToolTagStyles["grep"])},
		{name: "tool border read", got: styleSnapshot(steiner.ToolBorderStyles["read"]), want: styleSnapshot(build.ToolBorderStyles["read"])},
		{name: "delegate tag research", got: styleSnapshot(steiner.DelegateTagStyles["research"]), want: styleSnapshot(build.DelegateTagStyles["research"])},
		{name: "delegate border plan", got: styleSnapshot(steiner.DelegateBorderStyles["plan"]), want: styleSnapshot(build.DelegateBorderStyles["plan"])},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("steiner snapshot = %q, want %q", tt.got, tt.want)
			}
		})
	}
}
