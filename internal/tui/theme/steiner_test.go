package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
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
