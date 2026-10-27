package builtin

import (
	"fmt"
)

const (
	// bashArtifactMaxBytes is the largest body saveBashArtifact accepts.
	bashArtifactMaxBytes = 1048576
	// bashArtifactAttempts bounds retries on a (practically impossible) name collision.
	bashArtifactAttempts = 4
)

var bashArtifactDirs = []string{".steiner", "tmp", "bash-output"}

// saveBashArtifact writes body to a new private file under
// .steiner/tmp/bash-output/ inside workDir and returns its workDir-relative
// slash path.
//
// Guarantees (unix): every directory component is opened with O_NOFOLLOW
// relative to its parent descriptor, so a symlink at any component is refused
// (even one staying inside workDir). The file is created relative to the opened
// leaf directory descriptor, so it can never land in a directory substituted
// after the open. After writing, the path is re-resolved without following
// symlinks and must reach the same directory; otherwise the file is removed and
// an error is returned. The leaf directory is forced to mode 0700 (failing if
// it is not owned by the current user); .steiner and .steiner/tmp keep their
// existing permissions. Not guaranteed: if the directories are renamed away
// and back, or workDir itself is swapped, the check cannot tell. On platforms
// without unix openat semantics the function always fails.
func saveBashArtifact(workDir string, body []byte) (string, error) {
	if len(body) > bashArtifactMaxBytes {
		return "", fmt.Errorf("bash artifact too large: %d bytes exceeds %d", len(body), bashArtifactMaxBytes)
	}
	return saveBashArtifactPlatform(workDir, body)
}
