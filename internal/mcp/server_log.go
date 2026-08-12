package mcp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ServerLogPath derives the MCP server log path from the main session log path.
// Returns empty string when sessionLogPath is empty.
func ServerLogPath(sessionLogPath string) string {
	sessionLogPath = strings.TrimSpace(sessionLogPath)
	if sessionLogPath == "" {
		return ""
	}
	ext := filepath.Ext(sessionLogPath)
	base := strings.TrimSuffix(sessionLogPath, ext)
	if ext == "" {
		ext = ".log"
	}
	return base + "-mcp" + ext
}

// noOpWriteCloser is a no-op io.WriteCloser returned when no log path is provided.
type noOpWriteCloser struct{}

func (nw *noOpWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (nw *noOpWriteCloser) Close() error {
	return nil
}

// NewServerLogWriter creates an io.WriteCloser for MCP server stderr.
// When path is empty, returns a no-op WriteCloser that discards output.
// When path is provided, creates parent directories as needed and opens the file
// in append mode with mode 0o600.
func NewServerLogWriter(path string) (io.WriteCloser, error) {
	if strings.TrimSpace(path) == "" {
		return &noOpWriteCloser{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create mcp server log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mcp server log: %w", err)
	}
	return f, nil
}
