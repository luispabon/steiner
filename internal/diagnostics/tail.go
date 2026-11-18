package diagnostics

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// tailScanBytes bounds one read while truncateIncompleteTail searches backwards
// for the last complete line.
const tailScanBytes = 4096

// truncateIncompleteTail drops a trailing fragment left by a process killed
// mid-record. Appending after an unterminated line would merge the fragment with
// the next record and corrupt both, so the file is cut back to the last
// newline. Complete lines stay, and a file that already ends with a newline is
// left alone. Caller must hold the stream lock.
func truncateIncompleteTail(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("repair diagnostics stream tail: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("repair diagnostics stream tail: %w", err)
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	buf := make([]byte, tailScanBytes)
	for end := size; end > 0; {
		start := end - tailScanBytes
		if start < 0 {
			start = 0
		}
		n := int(end - start)
		if _, readErr := file.ReadAt(buf[:n], start); readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("repair diagnostics stream tail: %w", readErr)
		}
		end = start
		idx := bytes.LastIndexByte(buf[:n], '\n')
		if idx < 0 {
			continue
		}
		keep := start + int64(idx) + 1
		if keep == size {
			return nil
		}
		if err := file.Truncate(keep); err != nil {
			return fmt.Errorf("repair diagnostics stream tail: %w", err)
		}
		return nil
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("repair diagnostics stream tail: %w", err)
	}
	return nil
}
