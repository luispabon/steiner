package builtin

import (
	"fmt"
	"hash/crc32"
	"strings"
)

// FileContentHash returns a stable content hash for data, normalizing
// trailing whitespace per line so cosmetic edits (trailing space/CRLF)
// don't register as a content change. Shared by read/mutate's own file_hash
// fields and by the advisor tool's full-file dedup check
// (internal/advisor/files.go) — both must use the identical normalization
// so a hash computed by one matches a hash computed by the other for the
// same on-disk bytes.
func FileContentHash(data []byte) string {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\t \r")
	}
	normalized := strings.Join(lines, "\n")
	sum := crc32.ChecksumIEEE([]byte(normalized))
	return fmt.Sprintf("%08X", sum)
}
