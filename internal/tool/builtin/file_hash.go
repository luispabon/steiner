package builtin

import (
	"fmt"
	"hash/crc32"
	"strings"
)

func fileContentHash(data []byte) string {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\t \r")
	}
	normalized := strings.Join(lines, "\n")
	sum := crc32.ChecksumIEEE([]byte(normalized))
	return fmt.Sprintf("%08X", sum)
}
