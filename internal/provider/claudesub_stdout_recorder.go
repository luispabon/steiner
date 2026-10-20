package provider

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// claudeSubStdoutRecorder appends raw nonempty stdout lines to one per-session
// JSONL file. It is only created when STEINER_CLAUDESUB_RECORD is non-empty.
type claudeSubStdoutRecorder struct {
	mu        sync.Mutex
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

func newClaudeSubStdoutRecorder(root, sessionKey string) (*claudeSubStdoutRecorder, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create claude_subscription recording directory: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect claude_subscription recording directory: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("claude_subscription recording path is not a directory")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure claude_subscription recording directory: %w", err)
	}

	digest := sha256.Sum256([]byte(sessionKey))
	path := filepath.Join(root, fmt.Sprintf("%x.jsonl", digest))
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("claude_subscription recording file is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect claude_subscription recording file: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open claude_subscription recording file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure claude_subscription recording file: %w", err)
	}
	return &claudeSubStdoutRecorder{file: file}, nil
}

func (r *claudeSubStdoutRecorder) writeLine(line []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return errors.New("claude_subscription stdout recorder is closed")
	}
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	n, err := r.file.Write(buf)
	if err == nil && n != len(buf) {
		err = io.ErrShortWrite
	}
	return err
}

func (r *claudeSubStdoutRecorder) close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closeErr = r.file.Close()
		r.file = nil
		r.mu.Unlock()
	})
	return r.closeErr
}
