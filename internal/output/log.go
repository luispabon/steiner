package output

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
)

// CompactJSON renders a value as a single-line JSON string for diagnostics.
func CompactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// ConfigureLogger installs the process-wide slog handler. Output goes to w at
// the given level. Passing an io.Discard writer silences logging entirely.
// Callers must never pass os.Stderr while the TUI is live; the composition
// root is responsible for picking a destination that keeps stderr free.
func ConfigureLogger(w io.Writer, level string) {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	slog.SetDefault(slog.New(handler))
}

// SlogPath derives the sibling slog output path from the main session log
// path, mirroring how mcp.ServerLogPath and provider.StreamErrorLogPath
// derive theirs. Returns empty string when sessionLogPath is empty.
func SlogPath(sessionLogPath string) string {
	sessionLogPath = strings.TrimSpace(sessionLogPath)
	if sessionLogPath == "" {
		return ""
	}
	ext := filepath.Ext(sessionLogPath)
	base := strings.TrimSuffix(sessionLogPath, ext)
	return base + ".slog"
}

func parseLevel(level string) slog.Leveler {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return slog.Level(-8)
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}
