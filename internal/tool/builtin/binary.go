package builtin

import "bytes"

// isBinary checks if content contains null bytes in the first 8KB.
func isBinary(data []byte) bool {
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	return bytes.IndexByte(data[:checkLen], 0) >= 0
}

