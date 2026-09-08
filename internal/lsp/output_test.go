package lsp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
)

func TestFormatLocationsEmpty(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := Result{
		Locations:  []Location{},
		Incomplete: false,
		Truncated:  false,
	}

	output := formatLocations("/workspace", res, cfg)
	if output != "" {
		t.Errorf("formatLocations empty result: got %q, want empty", output)
	}
}

func TestFormatLocationsIncompleteEmpty(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100, ReadyTimeout: config.MustDuration("5s")}
	res := Result{
		Locations:  []Location{},
		Incomplete: true,
		Truncated:  false,
	}

	output := formatLocations("/workspace", res, cfg)
	if !strings.Contains(output, "indexing had not finished") {
		t.Errorf("formatLocations incomplete: message missing indexing note: %q", output)
	}
	if !strings.Contains(output, "5s") {
		t.Errorf("formatLocations incomplete: message missing timeout value: %q", output)
	}
}

func TestFormatLocationsSingle(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := Result{
		Locations: []Location{
			{File: "/workspace/src/main.go", Line: 10, Column: 5},
		},
		Incomplete: false,
		Truncated:  false,
	}

	output := formatLocations("/workspace", res, cfg)
	if !strings.Contains(output, "src/main.go:10:5") {
		t.Errorf("formatLocations single: expected src/main.go:10:5 in %q", output)
	}
	if strings.Contains(output, "/workspace/") {
		t.Errorf("formatLocations single: path should be relative, got %q", output)
	}
}

func TestFormatLocationsTruncated(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 10}
	res := Result{
		Locations: []Location{
			{File: "/workspace/a.go", Line: 1, Column: 1},
		},
		Incomplete: false,
		Truncated:  true,
		Total:      42,
	}

	output := formatLocations("/workspace", res, cfg)
	if !strings.Contains(output, "... 41 more results omitted (max_results=10)") {
		t.Errorf("formatLocations truncated: expected exact omission note in %q", output)
	}
}

func TestFormatLocationsFallbackAbsolute(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := Result{
		Locations: []Location{
			{File: "/other/path/main.go", Line: 1, Column: 1},
		},
		Incomplete: false,
		Truncated:  false,
	}

	output := formatLocations("/workspace", res, cfg)
	if !strings.Contains(output, "/other/path/main.go") {
		t.Errorf("formatLocations fallback: expected absolute path in %q", output)
	}
}

func TestFormatHoverEmpty(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100, ReadyTimeout: config.MustDuration("5s")}
	res := HoverResult{
		Content:    HoverContent{Text: ""},
		Incomplete: false,
	}

	output := formatHover(res, cfg)
	if output != "" {
		t.Errorf("formatHover empty result: got %q, want empty", output)
	}
}

func TestFormatHoverIncompleteEmpty(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100, ReadyTimeout: config.MustDuration("5s")}
	res := HoverResult{
		Content:    HoverContent{Text: ""},
		Incomplete: true,
	}

	output := formatHover(res, cfg)
	if !strings.Contains(output, "indexing had not finished") {
		t.Errorf("formatHover incomplete: message missing indexing note: %q", output)
	}
	if !strings.Contains(output, "5s") {
		t.Errorf("formatHover incomplete: message missing timeout value: %q", output)
	}
}

func TestFormatHoverWithContent(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := HoverResult{
		Content:    HoverContent{Text: "This is hover content"},
		Incomplete: false,
	}

	output := formatHover(res, cfg)
	if !strings.Contains(output, "This is hover content") {
		t.Errorf("formatHover: expected content in %q", output)
	}
}

func TestFormatHoverTruncation(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	// Create content that exceeds maxHoverChars (4000 chars).
	longContent := strings.Repeat("a", 4500)
	res := HoverResult{
		Content:    HoverContent{Text: longContent},
		Incomplete: false,
	}

	output := formatHover(res, cfg)
	if !strings.Contains(output, "... truncated (500 chars omitted)") {
		t.Errorf("formatHover: expected truncation note in %q", output)
	}
	if len([]rune(output)) > 4000+200 { // account for truncation message
		t.Errorf("formatHover: output too long after truncation: %d chars", len([]rune(output)))
	}
}

func TestFormatHoverTruncationRuneBoundary(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	// Create content with multi-byte characters near the truncation boundary.
	// Create 5000 runes total (exceeds maxHoverChars=4000).
	prefix := "x"                       // 1 rune
	middleChar := "é"                   // 1 rune (multi-byte in UTF-8)
	suffix := strings.Repeat("y", 4998) // 4998 runes

	longContent := prefix + middleChar + suffix

	res := HoverResult{
		Content:    HoverContent{Text: longContent},
		Incomplete: false,
	}

	output := formatHover(res, cfg)

	// Verify that the output is valid UTF-8 and doesn't split multi-byte runes.
	if !isValidUTF8(output) {
		t.Error("formatHover: output contains invalid UTF-8")
	}

	// Verify truncation happened.
	if !strings.Contains(output, "... truncated") {
		t.Errorf("formatHover: expected truncation note in %q", output)
	}
}

// isValidUTF8 checks if a string is valid UTF-8.
func isValidUTF8(s string) bool {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			return false
		}
		s = s[size:]
	}
	return true
}

func TestDiagnosticsOutputClean(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := DiagResult{
		Items:         []Diagnostic{},
		Truncated:     false,
		WindowExpired: false,
	}

	output := formatDiagnostics("/workspace", res, cfg)
	if !strings.Contains(output, "No diagnostics found.") {
		t.Errorf("formatDiagnostics clean: expected 'No diagnostics found.' in %q", output)
	}
	if strings.Contains(output, "collection window") {
		t.Errorf("formatDiagnostics clean: should not mention collection window, got %q", output)
	}
}

func TestDiagnosticsOutputProvisional(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := DiagResult{
		Items:         []Diagnostic{},
		Truncated:     false,
		WindowExpired: true,
	}

	output := formatDiagnostics("/workspace", res, cfg)
	if !strings.Contains(output, "collection window") {
		t.Errorf("formatDiagnostics provisional: expected 'collection window' phrase in %q", output)
	}
	if !strings.Contains(output, "state is unknown") {
		t.Errorf("formatDiagnostics provisional: expected 'state is unknown' in %q", output)
	}
	if strings.Contains(output, "No diagnostics found.") {
		t.Errorf("formatDiagnostics provisional: should not say 'No diagnostics found.', got %q", output)
	}
}

func TestDiagnosticsOutputDistinguishesCleanFromProvisional(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}

	cleanRes := DiagResult{
		Items:         []Diagnostic{},
		Truncated:     false,
		WindowExpired: false,
	}
	cleanOutput := formatDiagnostics("/workspace", cleanRes, cfg)

	provisionalRes := DiagResult{
		Items:         []Diagnostic{},
		Truncated:     false,
		WindowExpired: true,
	}
	provisionalOutput := formatDiagnostics("/workspace", provisionalRes, cfg)

	if cleanOutput == provisionalOutput {
		t.Errorf("clean and provisional outputs are identical: %q", cleanOutput)
	}

	if !strings.Contains(cleanOutput, "No diagnostics found.") {
		t.Errorf("clean output missing 'No diagnostics found.': %q", cleanOutput)
	}

	if !strings.Contains(provisionalOutput, "collection window") {
		t.Errorf("provisional output missing 'collection window': %q", provisionalOutput)
	}

	if strings.Contains(cleanOutput, "collection window") {
		t.Errorf("clean output should not contain 'collection window': %q", cleanOutput)
	}

	if strings.Contains(provisionalOutput, "No diagnostics found.") {
		t.Errorf("provisional output should not contain 'No diagnostics found.': %q", provisionalOutput)
	}
}

func TestDiagnosticsWithItems(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := DiagResult{
		Items: []Diagnostic{
			{
				File:     "/workspace/main.go",
				Line:     5,
				Column:   10,
				Severity: "error",
				Message:  "undefined variable",
				Source:   "gopls",
				Code:     "undeclaredName",
			},
		},
		Truncated:     false,
		WindowExpired: false,
	}

	output := formatDiagnostics("/workspace", res, cfg)
	if !strings.Contains(output, "main.go:5:10") {
		t.Errorf("formatDiagnostics with items: expected main.go:5:10 in %q", output)
	}
	if !strings.Contains(output, "error") {
		t.Errorf("formatDiagnostics with items: expected 'error' severity in %q", output)
	}
	if !strings.Contains(output, "undefined variable") {
		t.Errorf("formatDiagnostics with items: expected message in %q", output)
	}
	if !strings.Contains(output, "[gopls/undeclaredName]") {
		t.Errorf("formatDiagnostics with items: expected [gopls/undeclaredName] in %q", output)
	}
}

func TestDiagnosticsOmitSourceAndCodeWhenEmpty(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 100}
	res := DiagResult{
		Items: []Diagnostic{
			{
				File:     "/workspace/main.go",
				Line:     5,
				Column:   10,
				Severity: "warning",
				Message:  "unused variable",
				Source:   "",
				Code:     "",
			},
		},
		Truncated:     false,
		WindowExpired: false,
	}

	output := formatDiagnostics("/workspace", res, cfg)
	if strings.Contains(output, "[]") {
		t.Errorf("formatDiagnostics: should not render empty brackets, got %q", output)
	}
	if !strings.Contains(output, "main.go:5:10 warning: unused variable") {
		t.Errorf("formatDiagnostics: expected main.go:5:10 warning: unused variable in %q", output)
	}
}

func TestDiagnosticsTruncated(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 10}
	res := DiagResult{
		Items: []Diagnostic{
			{
				File:     "/workspace/main.go",
				Line:     1,
				Column:   1,
				Severity: "error",
				Message:  "test",
			},
		},
		Truncated:     true,
		WindowExpired: false,
		Total:         13,
	}

	output := formatDiagnostics("/workspace", res, cfg)
	if !strings.Contains(output, "... 12 more diagnostics omitted (max_results=10)") {
		t.Errorf("formatDiagnostics truncated: expected exact omission note in %q", output)
	}
}

func TestFormatOmittedCounts(t *testing.T) {
	cfg := config.LSPConfig{MaxResults: 2}

	locations := Result{
		Locations: []Location{
			{File: "/workspace/a.go", Line: 1, Column: 1},
			{File: "/workspace/b.go", Line: 2, Column: 1},
		},
		Truncated: true,
		Total:     3,
	}
	if got, want := formatLocations("/workspace", locations, cfg), "... 1 more results omitted (max_results=2)"; !strings.Contains(got, want) {
		t.Errorf("formatLocations = %q, want it to contain %q", got, want)
	}

	diags := DiagResult{
		Items: []Diagnostic{
			{File: "/workspace/a.go", Line: 1, Column: 1, Severity: "error", Message: "one"},
			{File: "/workspace/a.go", Line: 2, Column: 1, Severity: "error", Message: "two"},
		},
		Truncated: true,
		Total:     3,
	}
	if got, want := formatDiagnostics("/workspace", diags, cfg), "... 1 more diagnostics omitted (max_results=2)"; !strings.Contains(got, want) {
		t.Errorf("formatDiagnostics = %q, want it to contain %q", got, want)
	}
}

func TestMakeRelative(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		absPath string
		want    string
	}{
		{
			name:    "direct child",
			root:    "/workspace",
			absPath: "/workspace/main.go",
			want:    "main.go",
		},
		{
			name:    "nested path",
			root:    "/workspace",
			absPath: "/workspace/src/pkg/main.go",
			want:    "src/pkg/main.go",
		},
		{
			name:    "outside root",
			root:    "/workspace",
			absPath: "/other/file.go",
			want:    "/other/file.go",
		},
		{
			name:    "empty root",
			root:    "",
			absPath: "/workspace/main.go",
			want:    "/workspace/main.go",
		},
		{
			name:    "empty path",
			root:    "/workspace",
			absPath: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRelative(tt.root, tt.absPath)
			if got != tt.want {
				t.Errorf("makeRelative(%q, %q) = %q, want %q", tt.root, tt.absPath, got, tt.want)
			}
		})
	}
}
