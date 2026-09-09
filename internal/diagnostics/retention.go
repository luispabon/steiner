package diagnostics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// pruneStale drops records older than cutoff from the stream file at path.
// Records are appended in time order, so only the first line has to be read to
// decide: when it is still within the retention window nothing is rewritten.
// Rotated generations are left alone; they are already bounded by count.
func pruneStale(path string, cutoff time.Time) error {
	stale, err := oldestIsStale(path, cutoff)
	if err != nil || !stale {
		return err
	}
	return rewriteAfter(path, cutoff)
}

// pruneStaleGenerations applies retention to the active stream and every
// rotated generation. Each generation has its own oldest record, so checking
// only the active file would leave expired records in rotated files.
func pruneStaleGenerations(path string, cutoff time.Time) error {
	for i := maxStreamGenerations; i >= 1; i-- {
		if err := pruneStale(fmt.Sprintf("%s.%d", path, i), cutoff); err != nil {
			return err
		}
	}
	return pruneStale(path, cutoff)
}

// oldestIsStale reports whether the file's first record predates cutoff. A
// missing or empty file is never stale.
func oldestIsStale(path string, cutoff time.Time) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open diagnostics stream for retention: %w", err)
	}
	defer func() { _ = file.Close() }()

	line, err := bufio.NewReader(file).ReadBytes('\n')
	if len(line) == 0 {
		if err == nil || errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("read diagnostics stream for retention: %w", err)
	}
	ts, ok := recordTimestamp(line)
	// An undatable first line is treated as stale so the rewrite can drop it;
	// a file that cannot be dated cannot be retained on age.
	return !ok || ts.Before(cutoff), nil
}

// rewriteAfter writes every line at or after cutoff to a temporary file in the
// same directory and renames it over path. Lines that cannot be dated are
// dropped: retention cannot be enforced on a record with no timestamp.
func rewriteAfter(path string, cutoff time.Time) error {
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open diagnostics stream for retention: %w", err)
	}
	defer func() { _ = src.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".prune-*")
	if err != nil {
		return fmt.Errorf("create diagnostics retention temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set diagnostics retention temp file mode: %w", err)
	}

	reader := bufio.NewReader(src)
	writer := bufio.NewWriter(tmp)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if ts, ok := recordTimestamp(line); ok && !ts.Before(cutoff) {
				if _, err := writer.Write(withNewline(line)); err != nil {
					return fmt.Errorf("write diagnostics retention temp file: %w", err)
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fmt.Errorf("read diagnostics stream for retention: %w", readErr)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush diagnostics retention temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close diagnostics retention temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace diagnostics stream after retention: %w", err)
	}
	return nil
}

func withNewline(line []byte) []byte {
	if line[len(line)-1] == '\n' {
		return line
	}
	return append(line, '\n')
}

func recordTimestamp(line []byte) (time.Time, bool) {
	var head struct {
		Timestamp time.Time `json:"ts"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Timestamp.IsZero() {
		return time.Time{}, false
	}
	return head.Timestamp, true
}
