package theme

import (
	"strings"
	"testing"

	"charm.land/glamour/v2"
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
		{name: "delegate tag default", got: styleSnapshot(steiner.DelegateTagDefault), want: styleSnapshot(build.DelegateTagDefault)},
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
