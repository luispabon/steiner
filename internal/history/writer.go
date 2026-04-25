package history

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Writer struct {
	mu   sync.Mutex
	file *os.File
	path string
}

func NewWriter(path string) (*Writer, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &Writer{file: f, path: path}, nil
}

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

func (w *Writer) TrimAfterAppend(max int) error {
	if max <= 0 {
		return nil
	}
	w.file.Seek(0, os.SEEK_SET)
	var lines []string
	scanner := bufio.NewScanner(w.file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(lines) <= max {
		w.file.Seek(0, os.SEEK_SET)
		return nil
	}
	lines = lines[len(lines)-max:]
	tmpPath := w.path + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	for _, line := range lines {
		if _, err := tmp.WriteString(line + "\n"); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, w.path); err != nil {
		return err
	}
	w.file, err = os.OpenFile(w.path, os.O_RDWR|os.O_APPEND, 0644)
	return err
}

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

func (w *Writer) Path() string {
	return w.path
}

func (w *Writer) Load() ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file.Seek(0, os.SEEK_SET)
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