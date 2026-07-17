// Package history persists local conversation history.
package history

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Writer persists prompt history to a local file.
type Writer struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewWriter opens or creates a history file at the given path.
func NewWriter(path string) (*Writer, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{file: f, path: path}, nil
}

// Record appends a prompt to the history file and trims the file to the recent limit.
func (w *Writer) Record(prompt string) error {
	if prompt == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	escaped := strings.ReplaceAll(prompt, "\t", "\\t")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	line := time.Now().Format(time.RFC3339) + "\t" + escaped + "\n"
	_, err := w.file.WriteString(line)
	if err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	return w.TrimAfterAppend(50)
}

// TrimAfterAppend keeps only the most recent max history entries after an append.
func (w *Writer) TrimAfterAppend(max int) error {
	if max <= 0 {
		return nil
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek to beginning of file: %w", err)
	}
	var lines []string
	scanner := bufio.NewScanner(w.file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(lines) <= max {
		if _, err := w.file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek to beginning of file: %w", err)
		}
		return nil
	}
	lines = lines[len(lines)-max:]
	tmpPath := w.path + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	for _, line := range lines {
		if _, err := tmp.WriteString(line + "\n"); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, w.path); err != nil {
		return err
	}
	newFile, err := os.OpenFile(w.path, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	oldFile := w.file
	w.file = newFile
	if closeErr := oldFile.Close(); closeErr != nil {
		return fmt.Errorf("close previous history file handle: %w", closeErr)
	}
	return nil
}

// Close closes the underlying history file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Path returns the configured history file path.
func (w *Writer) Path() string {
	return w.path
}

// Load reads the stored prompts from the history file.
func (w *Writer) Load() ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to beginning of file: %w", err)
	}
	var prompts []string
	scanner := bufio.NewScanner(w.file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		prompt := parts[1]
		prompt = strings.ReplaceAll(prompt, "\\t", "\t")
		prompt = strings.ReplaceAll(prompt, "\\n", "\n")
		prompts = append(prompts, prompt)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return prompts, nil
}
