package builtin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// maxReadFileBytes caps the size of text files the read tool accepts. The size
// is checked on the opened file for an early rejection, then the read itself is
// capped so a file that grows after that check still cannot exceed the limit.
const maxReadFileBytes = 1024 * 1024

func readTextRange(path string, offset, limit int) (fileHash string, totalLines int, page []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, []string{fmt.Sprintf("Error reading file: %v", err)}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", 0, []string{fmt.Sprintf("Error reading file: %v", err)}, err
	}
	return readOpenTextRange(f, info.Size(), offset, limit)
}

// readOpenTextRange reads the open text file r, whose stat size is statSize,
// and returns the requested page with whole-file metadata. The stat size is
// checked first so an oversize file is rejected without reading it. readUpTo
// still caps the read, so a file that grew after the stat is rejected too.
func readOpenTextRange(r io.Reader, statSize int64, offset, limit int) (fileHash string, totalLines int, page []string, err error) {
	if statSize > maxReadFileBytes {
		return oversizeRead(oversizeSizeDetail(statSize))
	}
	data, exceeded, err := readUpTo(r, maxReadFileBytes)
	if err != nil {
		return "", 0, nil, err
	}
	if isBinary(data[:min(len(data), 8192)]) {
		err = fmt.Errorf("binary file")
		return "", 0, []string{"Error: cannot read binary file"}, err
	}
	if exceeded {
		return oversizeRead("file grew past the limit while reading")
	}
	fileHash, totalLines = hashAndCountLinesBytes(data)
	lines := splitReadLines(data)
	start := min(offset-1, len(lines))
	end := min(start+limit, len(lines))
	return fileHash, totalLines, lines[start:end], nil
}

// readUpTo reads at most limit+1 bytes from r, so the caller can detect input
// over limit without buffering all of it. exceeded reports whether r held more
// than limit bytes; data then holds limit+1 bytes.
func readUpTo(r io.Reader, limit int64) (data []byte, exceeded bool, err error) {
	data, err = io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	return data, int64(len(data)) > limit, nil
}

// oversizeRead returns the read result for a text file larger than
// maxReadFileBytes. detail describes the size, shown after the limit.
func oversizeRead(detail string) (string, int, []string, error) {
	msg := fmt.Sprintf("Error: file too large (max %s, %s); offset/limit cannot bypass this limit, use grep or bash to inspect it", output.FormatFileSize(maxReadFileBytes), detail)
	return "", 0, []string{msg}, errors.New("file too large")
}

// oversizeSizeDetail describes a stat size over the limit. FormatFileSize
// rounds to one decimal place, so a size just over the limit would read as the
// limit itself; such sizes are shown in exact bytes.
func oversizeSizeDetail(size int64) string {
	formatted := output.FormatFileSize(int(size))
	if formatted == output.FormatFileSize(maxReadFileBytes) {
		return fmt.Sprintf("got %d bytes", size)
	}
	return "got " + formatted
}

func splitReadLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
