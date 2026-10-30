//go:build !unix

package oauth

import "io/fs"

// fileInode reports 0: this platform exposes no stable inode, so file stamps
// fall back to size and modification time.
func fileInode(fs.FileInfo) uint64 { return 0 }
