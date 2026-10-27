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

var bashArtifactDirs = []string{toolArtifactSteinerDir, toolArtifactTmpDir, bashArtifactDir}

// saveBashArtifact writes body to a new private file under
// .steiner/tmp/bash-output/ inside workDir and returns its workDir-relative
// slash path.
//
// Guarantees (unix): every directory component is opened with O_NOFOLLOW
// relative to its parent descriptor, so a symlink at any component is refused
// (even one staying inside workDir). The file is created relative to the opened
// leaf directory descriptor, so it can never land in a directory substituted
// after the open. After writing, the path is re-resolved without following
// symlinks and must reach the same directory, and the file name must still
// refer to the same regular-file inode that was written; otherwise an error is
// returned. Cleanup unlinks only a name that still refers to our own inode. The leaf directory is forced to mode 0700 (failing if
// it is not owned by the current user); .steiner and .steiner/tmp keep their
// existing permissions. Not guaranteed: if the directories are renamed away
// and back, or workDir itself is swapped, the check cannot tell. On non-unix
// platforms the function always fails: os.Root follows in-root symlinks and
// Go exposes no portable no-follow directory open there, so no-redirection
// cannot be guaranteed. Escalate if non-unix support is required.
func saveBashArtifact(workDir string, body []byte) (string, error) {
	return (&bashArtifactStore{workDir: workDir}).save(body)
}

// bashArtifactStore saves artifacts under workDir. hook is a test seam called
// at named stages ("before-create", "before-verify") with the artifact file
// name (empty before creation); it is nil in production.
type bashArtifactStore struct {
	workDir string
	hook    func(stage, name string)
}

func (s *bashArtifactStore) runHook(stage, name string) {
	if s.hook != nil {
		s.hook(stage, name)
	}
}

func (s *bashArtifactStore) save(body []byte) (string, error) {
	if len(body) > bashArtifactMaxBytes {
		return "", fmt.Errorf("bash artifact too large: %d bytes exceeds %d", len(body), bashArtifactMaxBytes)
	}
	return s.savePlatform(body)
}
