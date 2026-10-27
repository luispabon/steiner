//go:build unix

package builtin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// bashArtifactHook is a test seam called at named stages ("before-create",
// "before-verify"). It is nil in production.
var bashArtifactHook func(stage string)

func runBashArtifactHook(stage string) {
	if bashArtifactHook != nil {
		bashArtifactHook(stage)
	}
}

func saveBashArtifactPlatform(workDir string, body []byte) (string, error) {
	leaf, err := openArtifactLeaf(workDir, true)
	if err != nil {
		return "", err
	}
	defer unix.Close(leaf) //nolint:errcheck // read-only descriptor close

	var st unix.Stat_t
	if err := unix.Fstat(leaf, &st); err != nil {
		return "", fmt.Errorf("stat bash artifact dir: %w", err)
	}
	if int(st.Uid) != os.Geteuid() {
		return "", errors.New("bash artifact dir is not owned by the current user")
	}
	if st.Mode&0o7777 != 0o700 {
		if err := unix.Fchmod(leaf, 0o700); err != nil {
			return "", fmt.Errorf("tighten bash artifact dir mode: %w", err)
		}
	}

	runBashArtifactHook("before-create")
	for range bashArtifactAttempts {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("generate bash artifact name: %w", err)
		}
		name := hex.EncodeToString(buf[:]) + ".log"
		fd, err := unix.Openat(leaf, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create bash artifact: %w", err)
		}
		return finishArtifact(workDir, leaf, &st, fd, name, body)
	}
	return "", errors.New("create bash artifact: name collisions exhausted")
}

// finishArtifact writes body, then verifies the artifact path still resolves
// (without following symlinks) to the directory the file was created in.
func finishArtifact(workDir string, leaf int, leafStat *unix.Stat_t, fd int, name string, body []byte) (string, error) {
	f := os.NewFile(uintptr(fd), name)
	remove := func() { _ = unix.Unlinkat(leaf, name, 0) } // best-effort cleanup of our own file
	if _, err := f.Write(body); err != nil {
		_ = f.Close() // best-effort cleanup after write failure
		remove()
		return "", fmt.Errorf("write bash artifact: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", fmt.Errorf("close bash artifact: %w", err)
	}

	runBashArtifactHook("before-verify")
	check, err := openArtifactLeaf(workDir, false)
	if err != nil {
		remove()
		return "", fmt.Errorf("verify bash artifact path: %w", err)
	}
	defer unix.Close(check) //nolint:errcheck // read-only descriptor close
	var cs unix.Stat_t
	if err := unix.Fstat(check, &cs); err != nil {
		remove()
		return "", fmt.Errorf("verify bash artifact path: %w", err)
	}
	if cs.Dev != leafStat.Dev || cs.Ino != leafStat.Ino {
		remove()
		return "", errors.New("bash artifact directory was replaced during write; artifact discarded")
	}
	return path.Join(path.Join(bashArtifactDirs...), name), nil
}

// openArtifactLeaf opens workDir and walks bashArtifactDirs with
// O_NOFOLLOW|O_DIRECTORY relative to each parent descriptor, optionally
// creating missing directories with mode 0700. It returns a descriptor for the
// leaf directory. workDir itself is the trust anchor and may be a symlink.
func openArtifactLeaf(workDir string, create bool) (int, error) {
	cur, err := unix.Open(workDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open bash artifact root: %w", err)
	}
	for _, part := range bashArtifactDirs {
		next, err := openDirNoFollow(cur, part, create)
		_ = unix.Close(cur) // parent descriptor no longer needed
		if err != nil {
			return -1, err
		}
		cur = next
	}
	return cur, nil
}

func openDirNoFollow(parent int, name string, create bool) (int, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(parent, name, flags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		if err := unix.Mkdirat(parent, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create bash artifact dir %s: %w", name, err)
		}
		fd, err = unix.Openat(parent, name, flags, 0)
	}
	if err != nil {
		return -1, fmt.Errorf("open bash artifact dir %s (symlinks and non-directories are refused): %w", name, err)
	}
	return fd, nil
}
