package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
)

const (
	maxHoverChars = 4000
	// maxItemChars caps each server-supplied string field (message, source, code, name).
	maxItemChars = 1000
	// maxOutputBytes caps the total size of a formatted locations/diagnostics/symbols result.
	maxOutputBytes = 64 * 1024
)

// truncateRunes shortens text to at most limit runes, appending an omission note.
func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + fmt.Sprintf("\n... truncated (%d chars omitted)", len(runes)-limit)
}

// capField bounds one server-supplied field to maxItemChars, flattening the
// truncation note onto a single line so it cannot break the one-item-per-line layout.
func capField(text string) string {
	return strings.ReplaceAll(truncateRunes(text, maxItemChars), "\n... truncated", " ... truncated")
}

// capOutput bounds joined output to maxOutputBytes on a rune boundary.
func capOutput(out string) string {
	if len(out) <= maxOutputBytes {
		return out
	}
	cut := maxOutputBytes
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + fmt.Sprintf("\n... output truncated (%d bytes omitted)", len(out)-cut)
}

// formatLocations renders Result locations as relative paths in a bounded output string.
// When Truncated is set, appends an omission note; when Incomplete is set, prepends
// an indexing note. Returns the formatted string or an empty string if no locations.
func formatLocations(workspace string, res Result, cfg config.LSPConfig) string {
	var parts []string

	if res.Incomplete {
		parts = append(parts, fmt.Sprintf("Workspace indexing had not finished within %s; results may be incomplete.", cfg.ReadyTimeout))
		parts = append(parts, "")
	}

	if len(res.Locations) == 0 && res.Incomplete {
		return strings.Join(parts, "\n")
	}

	for _, loc := range res.Locations {
		relPath := makeRelative(workspace, loc.File)
		parts = append(parts, fmt.Sprintf("%s:%d:%d", relPath, loc.Line, loc.Column))
	}

	if res.Truncated {
		parts = append(parts, fmt.Sprintf("... %d more results omitted (max_results=%d)", res.Total-len(res.Locations), cfg.MaxResults))
	}

	return capOutput(strings.Join(parts, "\n"))
}

// formatDiagnostics renders DiagResult items as relative paths with severity and message.
// Handles the three-way distinction: clean file, provisional empty, or items present.
// When WindowExpired is true with zero items, renders a provisional message.
// When WindowExpired is false with zero items, renders a clean message.
func formatDiagnostics(workspace string, res DiagResult, cfg config.LSPConfig) string {
	var parts []string

	if len(res.Items) == 0 {
		if res.WindowExpired {
			parts = append(parts, "No diagnostics received within the collection window; the file's state is unknown (server may still be indexing or slow to respond).")
		} else {
			parts = append(parts, "No diagnostics found.")
		}
		return strings.Join(parts, "\n")
	}

	for _, diag := range res.Items {
		relPath := makeRelative(workspace, diag.File)

		severity := diag.Severity
		if severity == "" {
			severity = "info"
		}

		line := fmt.Sprintf("%s:%d:%d %s: %s", relPath, diag.Line, diag.Column, severity, capField(diag.Message))

		if diag.Source != "" || diag.Code != "" {
			var tags []string
			if diag.Source != "" {
				tags = append(tags, capField(diag.Source))
			}
			if diag.Code != "" {
				tags = append(tags, capField(diag.Code))
			}
			line = line + " [" + strings.Join(tags, "/") + "]"
		}

		parts = append(parts, line)
	}

	if res.Truncated {
		parts = append(parts, fmt.Sprintf("... %d more diagnostics omitted (max_results=%d)", res.Total-len(res.Items), cfg.MaxResults))
	}

	return capOutput(strings.TrimSpace(strings.Join(parts, "\n")))
}

// formatSymbols renders SymbolResult symbols as relative paths in a bounded output string.
// When Truncated is set, appends an omission note; when Incomplete is set, prepends
// an indexing note. Returns the formatted string or an empty string if no symbols.
func formatSymbols(workspace string, res SymbolResult, cfg config.LSPConfig) string {
	var parts []string

	if res.Incomplete {
		parts = append(parts, fmt.Sprintf("Workspace indexing had not finished within %s; results may be incomplete.", cfg.ReadyTimeout))
		parts = append(parts, "")
	}

	if len(res.Symbols) == 0 && res.Incomplete {
		return strings.Join(parts, "\n")
	}

	for _, sym := range res.Symbols {
		relPath := makeRelative(workspace, sym.File)
		line := fmt.Sprintf("%s:%d:%d  %s  %s", relPath, sym.Line, sym.Column, sym.Kind, capField(sym.Name))
		if sym.Container != "" {
			line += fmt.Sprintf("  (in %s)", capField(sym.Container))
		}
		parts = append(parts, line)
	}

	if res.Truncated {
		parts = append(parts, fmt.Sprintf("... %d more results omitted (max_results=%d)", res.Total-len(res.Symbols), cfg.MaxResults))
	}

	return capOutput(strings.Join(parts, "\n"))
}

// makeRelative converts an absolute path to be relative to root, falling back to the
// absolute path if relativization fails or produces a path starting with "..".
func makeRelative(root, absPath string) string {
	if root == "" || absPath == "" {
		return absPath
	}

	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return absPath
	}

	if strings.HasPrefix(rel, "..") {
		return absPath
	}

	return rel
}

// formatHover renders HoverResult content with optional incompleteness note and truncation on rune boundaries.
func formatHover(res HoverResult, cfg config.LSPConfig) string {
	var parts []string
	if res.Incomplete {
		parts = append(parts, fmt.Sprintf("Workspace indexing had not finished within %s; results may be incomplete.", cfg.ReadyTimeout))
		parts = append(parts, "")
	}

	text := strings.TrimSpace(res.Content.Text)
	if text == "" {
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		return ""
	}

	parts = append(parts, truncateRunes(text, maxHoverChars))
	return strings.Join(parts, "\n")
}
