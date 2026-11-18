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

// repairIncompleteTail reports whether the active stream at path ends with an
// unterminated fragment, truncating it when it does. The last byte is read
// first, so a stream that already ends with a newline costs one open, one stat
// and one byte read, and an empty stream is nothing to repair. Append runs it
// under the stream lock before every record, so a fragment another process left
// after this writer opened is removed before a record can be merged with it.
// Caller must hold the stream lock.
func repairIncompleteTail(path string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect diagnostics stream tail: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect diagnostics stream tail: %w", err)
	}
	if info.Size() == 0 {
		return false, nil
	}
	var last [1]byte
	n, err := file.ReadAt(last[:], info.Size()-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("inspect diagnostics stream tail: %w", err)
	}
	if n == 0 || last[0] == '\n' {
		// An empty read means another holder truncated the file between the stat
		// and the read, so there is no fragment left to remove.
		return false, nil
	}
	if err := truncateIncompleteTail(path); err != nil {
		return false, err
	}
	return true, nil
}

// truncateIncompleteTail drops a trailing fragment left by a process killed
// mid-record. Appending after an unterminated line would merge the fragment with
// the next record and corrupt both, so the file is cut back to the last
// newline. Complete lines stay, and a file that already ends with a newline is
// left alone. Writer runs it when a stream is prepared and repairIncompleteTail
// runs it before every append. Caller must hold the stream lock.
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
		read, readErr := file.ReadAt(buf[:n], start)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("repair diagnostics stream tail: %w", readErr)
		}
		end = start
		if read == 0 {
			// A short read after the file shrank means nothing is left to scan.
			break
		}
		// Scan only the bytes actually read: a short read leaves stale bytes from
		// the previous window in buf, and a newline that is no longer in the file
		// would truncate the stream back to a larger size.
		idx := bytes.LastIndexByte(buf[:read], '\n')
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
