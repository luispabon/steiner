package builtin

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func readTextRange(path string, offset, limit int) (fileHash string, totalLines int, page []string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, []string{fmt.Sprintf("Error reading file: %v", err)}, err
	}
	if info.Size() > 100*1024 {
		err = fmt.Errorf("file too large")
		return "", 0, []string{fmt.Sprintf("Error: file too large (max 100KB, got %s)", output.FormatFileSize(int(info.Size())))}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, []string{fmt.Sprintf("Error reading file: %v", err)}, err
	}
	defer func() { _ = f.Close() }()
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
	data, err := io.ReadAll(f)
	if err != nil {
		return "", 0, nil, err
	}
	fileHash, totalLines = hashAndCountLinesBytes(data)
	lines := splitReadLines(data)
	start := min(offset-1, len(lines))
	end := min(start+limit, len(lines))
	return fileHash, totalLines, lines[start:end], nil
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
