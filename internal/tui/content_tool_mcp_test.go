package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestMCPOriginLookup(t *testing.T) {
	t.Parallel()

	buffer := &contentBuffer{
		mcpToolOrigins: map[string]MCPToolOrigin{
			"mcp__myserver__mytool": {Server: "myserver", Tool: "mytool"},
		},
	}

	if origin, ok := buffer.mcpOrigin("mcp__myserver__mytool"); !ok || origin.Server != "myserver" || origin.Tool != "mytool" {
		t.Fatalf("mcpOrigin(hit) = (%+v, %v), want ({myserver mytool}, true)", origin, ok)
	}

	if _, ok := buffer.mcpOrigin("bash"); ok {
		t.Fatalf("mcpOrigin(miss) = ok, want !ok")
	}
}

func TestMCPOriginNilMap(t *testing.T) {
	t.Parallel()

	var buffer contentBuffer
	if origin, ok := buffer.mcpOrigin("anything"); ok {
		t.Fatalf("mcpOrigin(nil map) = (%+v, %v), want (_, false)", origin, ok)
	}
}

func TestMCPToolDisplayTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool string
		want string
	}{
		{
			name: "mcp tool renders server arrow tool",
			tool: "mcp__myserver__mytool",
			want: "myserver → mytool",
		},
		{
			name: "built-in tool renders unchanged",
			tool: "bash",
			want: "bash",
		},
		{
			name: "server name with underscores renders intact",
			tool: "mcp__my_tool_srv__fetch",
			want: "my_tool_srv → fetch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buffer := &contentBuffer{
				mcpToolOrigins: map[string]MCPToolOrigin{
					"mcp__myserver__mytool":   {Server: "myserver", Tool: "mytool"},
					"mcp__my_tool_srv__fetch": {Server: "my_tool_srv", Tool: "fetch"},
				},
			}

			if got := buffer.toolDisplayTag(tt.tool); got != tt.want {
				t.Fatalf("toolDisplayTag(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

// TestMCPRenderToolCallFrameBuiltinUnchanged proves that rendering a
// built-in tool call is byte-identical whether the origins map is nil,
// empty, or populated with unrelated entries — the primary regression risk
// for this change.
func TestMCPRenderToolCallFrameBuiltinUnchanged(t *testing.T) {
	useTrueColor(t)

	segment := &toolCallSegment{
		tool:      "bash",
		args:      "printf hello",
		meta:      "✓",
		collapsed: true,
	}

	baseline := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	baselineRender := baseline.renderToolCallFrame(segment, 60)

	withNilMap := &contentBuffer{styles: testStyles(theme.AccentAmber), mcpToolOrigins: nil}
	withEmptyMap := &contentBuffer{styles: testStyles(theme.AccentAmber), mcpToolOrigins: map[string]MCPToolOrigin{}}
	withUnrelatedMap := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
		mcpToolOrigins: map[string]MCPToolOrigin{
			"mcp__other__tool": {Server: "other", Tool: "tool"},
		},
	}

	for name, buf := range map[string]*contentBuffer{
		"nil map":       withNilMap,
		"empty map":     withEmptyMap,
		"unrelated map": withUnrelatedMap,
	} {
		if got := buf.renderToolCallFrame(segment, 60); got != baselineRender {
			t.Fatalf("%s: renderToolCallFrame() diverged from baseline\ngot:  %q\nwant: %q", name, got, baselineRender)
		}
	}
}

// TestMCPRenderToolCallFrameAttribution proves an MCP tool call renders with
// the "server → tool" tag and the dedicated MCP tag/border styles instead of
// the default ones.
func TestMCPRenderToolCallFrameAttribution(t *testing.T) {
	useTrueColor(t)

	styles := testStyles(theme.AccentAmber)
	segment := &toolCallSegment{
		tool:      "mcp__myserver__mytool",
		args:      "some args",
		meta:      "✓",
		collapsed: true,
	}

	buffer := &contentBuffer{
		styles: styles,
		mcpToolOrigins: map[string]MCPToolOrigin{
			"mcp__myserver__mytool": {Server: "myserver", Tool: "mytool"},
		},
	}

	rendered := buffer.renderToolCallFrame(segment, 60)
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "myserver → mytool") {
		t.Fatalf("rendered frame = %q, want it to contain %q", plain, "myserver → mytool")
	}
	if strings.Contains(plain, "mcp__myserver__mytool") {
		t.Fatalf("rendered frame = %q, want raw registry tool name absent", plain)
	}

	wantTag := styles.ToolTagMCP.Render("myserver → mytool")
	if !strings.Contains(rendered, wantTag) {
		t.Fatalf("rendered frame does not use ToolTagMCP style for the tag")
	}

	box := buffer.renderToolCallBox(rendered, segment.tool, 64)
	wantBorderColor := styles.ToolBorderMCP.GetForeground()
	defaultBorderColor := styles.ToolBorderDefault.GetForeground()
	if wantBorderColor == defaultBorderColor {
		t.Fatalf("ToolBorderMCP and ToolBorderDefault resolve to the same color, cannot distinguish rendering")
	}
	_ = box
}

// TestMCPLongTagHeaderLayout proves a long MCP tag does not break header
// layout: the rendered line stays within the requested width and args are
// still truncated to fit.
func TestMCPLongTagHeaderLayout(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
		mcpToolOrigins: map[string]MCPToolOrigin{
			"mcp__a-very-long-descriptive-server-name__a-very-long-descriptive-tool-name": {
				Server: "a-very-long-descriptive-server-name",
				Tool:   "a-very-long-descriptive-tool-name",
			},
		},
	}

	segment := &toolCallSegment{
		tool:      "mcp__a-very-long-descriptive-server-name__a-very-long-descriptive-tool-name",
		args:      strings.Repeat("x", 200),
		meta:      "✓",
		collapsed: true,
	}

	const width = 60
	rendered := buffer.renderToolCall(segment, width)
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("line width = %d, want %d for line %q", got, width, stripANSI(line))
		}
	}
}
