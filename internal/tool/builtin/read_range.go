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
	if info.Size() > maxReadFileBytes {
		return oversizeRead(info.Size())
	}
	head := make([]byte, 8192)
	n, headErr := io.ReadFull(f, head)
	if headErr != nil && headErr != io.EOF && headErr != io.ErrUnexpectedEOF {
		return "", 0, nil, headErr
	}
	if isBinary(head[:n]) {
		err = fmt.Errorf("binary file")
		return "", 0, []string{"Error: cannot read binary file"}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, nil, err
	}
	data, exceeded, err := readUpTo(f, maxReadFileBytes)
	if err != nil {
		return "", 0, nil, err
	}
	if exceeded {
		return oversizeRead(int64(len(data)))
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
// maxReadFileBytes. size is the file size shown to the user.
func oversizeRead(size int64) (string, int, []string, error) {
	msg := fmt.Sprintf("Error: file too large (max %s, got %s); offset/limit cannot bypass this limit, use grep or bash to inspect it", output.FormatFileSize(maxReadFileBytes), output.FormatFileSize(int(size)))
	return "", 0, []string{msg}, errors.New("file too large")
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
