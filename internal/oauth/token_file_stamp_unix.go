//go:build unix

package oauth

import (
	"io/fs"
	"syscall"
)

// fileInode returns the inode of info, or 0 when the platform does not expose
// one through syscall.Stat_t.
func fileInode(info fs.FileInfo) uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return stat.Ino
}
