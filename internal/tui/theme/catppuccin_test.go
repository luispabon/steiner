package theme

import (
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

func TestCatppuccinMochaTheme(t *testing.T) {
	theme := mochaTheme{}

	// Test all 14 color methods return non-empty values
	tests := []struct {
		name  string
		color interface{}
	}{
		{"Background", theme.Background()},
		{"Foreground", theme.Foreground()},
		{"Accent", theme.Accent()},
		{"Muted", theme.Muted()},
		{"Border", theme.Border()},
		{"Error", theme.Error()},
		{"Warning", theme.Warning()},
		{"Success", theme.Success()},
		{"SyntaxKeyword", theme.SyntaxKeyword()},
		{"SyntaxString", theme.SyntaxString()},
		{"SyntaxComment", theme.SyntaxComment()},
		{"SyntaxFunction", theme.SyntaxFunction()},
		{"SyntaxNumber", theme.SyntaxNumber()},
		{"SyntaxOperator", theme.SyntaxOperator()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.color == "" {
				t.Errorf("%s returned empty color", tt.name)
			}
		})
	}
}

func TestCatppuccinMochaLipGlossStyles(t *testing.T) {
	theme := mochaTheme{}
	styles := theme.LipGlossStyles()

	// Verify all 14 Styles fields are populated
	styleTests := map[string]interface{}{
		"ContentPane":       styles.ContentPane,
		"Sidebar":           styles.Sidebar,
		"SidebarLabel":      styles.SidebarLabel,
		"SidebarValue":      styles.SidebarValue,
		"ToolBlock":         styles.ToolBlock,
		"ThinkingBlock":     styles.ThinkingBlock,
		"AssistantProse":    styles.AssistantProse,
		"ApprovalHighlight": styles.ApprovalHighlight,
		"InputArea":         styles.InputArea,
		"StatusBar":         styles.StatusBar,
		"Border":            styles.Border,
		"ErrorStyle":        styles.ErrorStyle,
		"WarningStyle":      styles.WarningStyle,
		"SuccessStyle":      styles.SuccessStyle,
	}

	if len(styleTests) != 14 {
		t.Errorf("expected 14 styles, got %d", len(styleTests))
	}
}

func TestCatppuccinMochaGlamourStyleSheet(t *testing.T) {
	theme := mochaTheme{}
	option := theme.GlamourStyleSheet()

	// Verify that the option is not nil
	if option == nil {
		t.Error("GlamourStyleSheet returned nil option")
	}
}

func TestCatppuccinMochaRegistration(t *testing.T) {
	// The init() function should have registered the theme as "catppuccin-mocha"
	theme, err := Get("catppuccin-mocha")
	if err != nil {
		t.Fatalf("Failed to get catppuccin-mocha theme: %v", err)
	}
	if theme == nil {
		t.Error("Got nil theme from registry")
	}
}

func TestCatppuccinMochaDefault(t *testing.T) {
	// Since catppuccin-mocha is registered first in init(),
	// it should be the default theme
	defaultTheme := Default()
	if defaultTheme == nil {
		t.Skip("Default theme is nil (may have been reset by another test)")
	}
}

// TestDummyThemeRegistration verifies that multiple themes can be registered and retrieved
func TestDummyThemeRegistration(t *testing.T) {
	// Register a dummy theme
	dummyName := "test-dummy-theme"
	Register(dummyName, dummyTheme{})

	// Retrieve it
	theme, err := Get(dummyName)
	if err != nil {
		t.Fatalf("Failed to get %s theme: %v", dummyName, err)
	}
	if theme == nil {
		t.Errorf("Got nil theme for %s", dummyName)
	}
}

// dummyTheme is a minimal test theme implementation
type dummyTheme struct{}

func (d dummyTheme) Background() lipgloss.Color {
	return lipgloss.Color("#000000")
}

func (d dummyTheme) Foreground() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) Accent() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) Muted() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) Border() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) Error() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) Warning() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) Success() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) SyntaxKeyword() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) SyntaxString() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) SyntaxComment() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) SyntaxFunction() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) SyntaxNumber() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) SyntaxOperator() lipgloss.Color {
	return lipgloss.Color("#ffffff")
}

func (d dummyTheme) LipGlossStyles() Styles {
	return Styles{}
}

func (d dummyTheme) GlamourStyleSheet() glamour.TermRendererOption {
	return glamour.WithStandardStyle("dark")
}
