package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestInferBodyKindJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		tool   string
		result string
		want   string
	}{
		{name: "mcp envelope with nested json string", tool: "mcp__server__tool", result: `{"ok":true,"result":"{\"name\":\"tool\"}"}`, want: "json"},
		{name: "mcp envelope with object result", tool: "mcp__server__tool", result: `{"ok":true,"result":{"name":"tool"}}`, want: "json"},
		{name: "mcp envelope with array result", tool: "mcp__server__tool", result: `{"ok":true,"result":[1,2]}`, want: "json"},
		{name: "mcp error envelope", tool: "mcp__server__tool", result: `{"ok":false,"error":{"kind":"mcp_tool_error","message":"failed"}}`, want: "json"},
		{name: "plain object", tool: "other", result: `{"name":"tool"}`, want: "json"},
		{name: "invalid json", tool: "mcp__server__tool", result: "not json", want: "plain"},
		{name: "mcp envelope missing error details", tool: "mcp__server__tool", result: `{"ok":false,"error":{}}`, want: "plain"},
		{name: "mcp envelope invalid nested json", tool: "mcp__server__tool", result: `{"ok":true,"result":"not json"}`, want: "plain"},
		{name: "mcp envelope scalar result", tool: "mcp__server__tool", result: `{"ok":true,"result":42}`, want: "plain"},
		{name: "trailing data", tool: "mcp__server__tool", result: `{"value":true} trailing`, want: "plain"},
		{name: "top-level scalar", tool: "mcp__server__tool", result: `"text"`, want: "plain"},
		{name: "nested result is scalar", tool: "mcp__server__tool", result: `{"ok":true,"result":"text"}`, want: "plain"},
		{name: "built-in precedence", tool: "bash", result: `{"name":"tool"}`, want: "bash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferBodyKind(tt.tool, tt.result); got != tt.want {
				t.Fatalf("inferBodyKind(%q, %q) = %q, want %q", tt.tool, tt.result, got, tt.want)
			}
		})
	}
}

func TestInferBodyKindJSONEnvelopeFallbacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result string
		want   string
	}{
		{name: "error message is not a string", result: `{"ok":false,"error":{"message":42}}`, want: "plain"},
		{name: "error is not an object", result: `{"ok":false,"error":"failed"}`, want: "plain"},
		{name: "successful envelope missing result", result: `{"ok":true}`, want: "json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferBodyKind("mcp__server__tool", tt.result); got != tt.want {
				t.Fatalf("inferBodyKind(%q) = %q, want %q", tt.result, got, tt.want)
			}
		})
	}
}

func TestBuildJSONLines(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	tests := []struct {
		name   string
		result string
		want   []string
	}{
		{
			name:   "object values and nested object",
			result: `{"ok":true,"result":"{\"id\":6388823,\"name\":\"Luis\",\"active\":true,\"empty\":null,\"details\":{\"city\":\"London\"}}"}`,
			want:   []string{"active: true", "details:", "  city: London", "empty: null", "id: 6388823", "name: Luis"},
		},
		{
			name:   "root array and item alignment",
			result: `{"ok":true,"result":"[{\"number\":545,\"state\":\"OPEN\",\"user\":{\"login\":\"luis\"}},{\"number\":544},{\"number\":543},{\"number\":542}]"}`,
			want:   []string{"- number: 545", "  state: OPEN", "  user:", "    login: luis", "- number: 544", "- number: 543", "+ 1 more items"},
		},
		{
			name:   "empty array",
			result: `{"ok":true,"result":{"items":[]}}`,
			want:   []string{"items: []"},
		},
		{
			name:   "error prefers message",
			result: `{"ok":false,"error":{"kind":"mcp_tool_error","message":"failed to list issues"}}`,
			want:   []string{"✗ failed to list issues"},
		},
		{
			name:   "error falls back to kind",
			result: `{"ok":false,"error":{"kind":"mcp_tool_error","message":"  "}}`,
			want:   []string{"✗ mcp_tool_error"},
		},
		{
			name:   "object with ok and unrelated field",
			result: `{"ok":true,"foo":"bar"}`,
			want:   []string{"foo: bar", "ok: true"},
		},
		{
			name:   "object with false ok and no error",
			result: `{"ok":false}`,
			want:   []string{"ok: false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &toolCallSegment{body: tt.result}
			got := make([]string, 0)
			for _, line := range b.buildJSONLines(tc) {
				got = append(got, stripANSI(line))
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("buildJSONLines() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildJSONLinesRootEmptyDocuments(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	for _, tt := range []struct {
		name   string
		result string
		want   []string
	}{
		{name: "empty object", result: `{}`, want: []string{"{}"}},
		{name: "empty array", result: `[]`, want: []string{"[]"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := make([]string, 0)
			for _, line := range b.buildJSONLines(&toolCallSegment{body: tt.result}) {
				got = append(got, stripANSI(line))
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("buildJSONLines() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildJSONLinesStringEscapesAndBoundsRows(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	lines := b.buildJSONLines(&toolCallSegment{body: `{"ok":true,"result":"{\"value\":\"line1\\nline2\\nline3\"}"}`})
	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		plain = append(plain, stripANSI(line))
	}
	if !slices.Equal(plain, []string{`value: line1\nline2\nline3`}) {
		t.Fatalf("buildJSONLines() = %q, want one escaped content line", plain)
	}
	if len(plain) > jsonBodyMaxLines+1 {
		t.Fatalf("rendered rows = %d, want <= %d including marker", len(plain), jsonBodyMaxLines+1)
	}
}

func TestJSONScalarTextTruncationBoundaries(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		count int
		want  string
	}{
		{name: "99 runes", count: 99, want: strings.Repeat("x", 99)},
		{name: "100 runes", count: 100, want: strings.Repeat("x", 100)},
		{name: "101 runes", count: 101, want: strings.Repeat("x", 99) + "…"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonScalarText(strings.Repeat("x", tt.count)); got != tt.want {
				t.Fatalf("jsonScalarText(%d runes) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

func TestBuildJSONLinesItemAndLineCaps(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	result := `{"items":[{"a":1,"b":2,"c":3},{"a":4,"b":5,"c":6},{"a":7,"b":8,"c":9},{"a":10}],"z":true}`
	lines := b.buildJSONLines(&toolCallSegment{body: result})
	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		plain = append(plain, stripANSI(line))
	}
	want := []string{
		"items:",
		"  - a: 1",
		"    b: 2",
		"    c: 3",
		"  - a: 4",
		"    b: 5",
		"    c: 6",
		"  - a: 7",
		"    b: 8",
		"    c: 9",
		"+ 2 more lines",
	}
	if !slices.Equal(plain, want) {
		t.Fatalf("buildJSONLines() = %q, want %q", plain, want)
	}
	if len(plain) > jsonBodyMaxLines+1 {
		t.Fatalf("rendered rows = %d, want <= %d including marker", len(plain), jsonBodyMaxLines+1)
	}
	for _, line := range plain[:jsonBodyMaxLines] {
		if strings.Contains(line, "more items") {
			t.Fatalf("item marker escaped line cap: %q", plain)
		}
	}
}

func TestBuildJSONLinesDimsKeysNotValues(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	lines := b.buildJSONLines(&toolCallSegment{body: `{"name":"value"}`})
	if len(lines) != 1 {
		t.Fatalf("rendered lines = %d, want 1", len(lines))
	}
	rendered := lines[0]
	dimmedKey := b.styles.FgDim.Render("name:")
	if !strings.Contains(dimmedKey, "\x1b[") {
		t.Fatalf("dimmed key = %q, want ANSI style escape", dimmedKey)
	}
	if !strings.Contains(rendered, dimmedKey) {
		t.Fatalf("rendered line = %q, want dimmed key %q", rendered, dimmedKey)
	}
	keyTextIndex := strings.Index(dimmedKey, "name:")
	if keyTextIndex < 0 {
		t.Fatalf("dimmed key = %q, want key text", dimmedKey)
	}
	keyReset := dimmedKey[keyTextIndex+len("name:"):]
	if keyReset == "" {
		t.Fatalf("dimmed key = %q, want reset after key", dimmedKey)
	}
	keyStart := strings.Index(rendered, dimmedKey)
	if keyStart < 0 {
		t.Fatalf("rendered line = %q, want dimmed key span", rendered)
	}
	keyTextEnd := keyStart + keyTextIndex + len("name:")
	resetStart := strings.Index(rendered[keyTextEnd:], keyReset)
	if resetStart < 0 {
		t.Fatalf("rendered line = %q, want reset after key", rendered)
	}
	resetEnd := keyTextEnd + resetStart + len(keyReset)
	valueStart := strings.Index(rendered, "value")
	if valueStart <= resetEnd {
		t.Fatalf("rendered line = %q, want value after key reset", rendered)
	}
	dimOpen := dimmedKey[:keyTextIndex]
	lastOpen := strings.LastIndex(rendered[:valueStart], dimOpen)
	lastReset := strings.LastIndex(rendered[:valueStart], keyReset)
	if lastOpen > lastReset {
		t.Fatalf("rendered line = %q, want dim style reset before value", rendered)
	}
}

func TestBuildJSONLinesBoundsAndTruncates(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	result := `{"ok":true,"result":{"a":"` + strings.Repeat("é", 101) + `","b":true,"c":null,"d":1,"e":2,"f":3,"g":4,"h":5,"i":6,"j":7,"k":8}}`
	lines := b.buildJSONLines(&toolCallSegment{body: result})
	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		plain = append(plain, stripANSI(line))
	}
	if len(plain) != 11 {
		t.Fatalf("line count = %d, want 11 including marker: %q", len(plain), plain)
	}
	if !strings.HasSuffix(plain[0], "…") || len([]rune(strings.TrimPrefix(plain[0], "a: "))) != 100 {
		t.Fatalf("truncated line = %q, want 99 runes plus ellipsis", plain[0])
	}
	if plain[10] != "+ 1 more lines" {
		t.Fatalf("cap marker = %q, want %q", plain[10], "+ 1 more lines")
	}
}

func TestBuildJSONLinesFallsBackToPlain(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	for _, result := range []string{"not json", `{"ok":true,"result":"text"}`, `42`} {
		t.Run(result, func(t *testing.T) {
			got := b.buildJSONLines(&toolCallSegment{body: result})
			if len(got) != 1 || stripANSI(got[0]) != result {
				t.Fatalf("buildJSONLines() = %q, want plain %q", got, result)
			}
		})
	}
}
