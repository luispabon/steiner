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

func (s *bashArtifactStore) savePlatform(body []byte) (string, error) {
	workDir := s.workDir
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

	s.runHook("before-create", "")
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
		return s.finishArtifact(leaf, &st, fd, name, body)
	}
	return "", errors.New("create bash artifact: name collisions exhausted")
}

// finishArtifact writes body, then verifies that the name still refers to the
// regular file we wrote and that the directory path still resolves (without
// following symlinks) to the directory it was created in. The file descriptor
// stays open until verification so its inode cannot be recycled.
func (s *bashArtifactStore) finishArtifact(leaf int, leafStat *unix.Stat_t, fd int, name string, body []byte) (string, error) {
	f := os.NewFile(uintptr(fd), name)
	defer f.Close() //nolint:errcheck // descriptor released after verification; write errors are checked via Sync/Write

	var fs unix.Stat_t
	if err := unix.Fstat(fd, &fs); err != nil {
		_ = unix.Unlinkat(leaf, name, 0) // just created by us, nothing could have replaced it yet in practice
		return "", fmt.Errorf("stat bash artifact: %w", err)
	}
	// removeOwn unlinks name only while it still refers to our inode.
	removeOwn := func() {
		if sameRegularFile(leaf, name, &fs) {
			_ = unix.Unlinkat(leaf, name, 0) // best-effort cleanup of our own file
		}
	}
	if _, err := f.Write(body); err != nil {
		removeOwn()
		return "", fmt.Errorf("write bash artifact: %w", err)
	}

	s.runHook("before-verify", name)
	if !sameRegularFile(leaf, name, &fs) {
		return "", errors.New("bash artifact was replaced or removed during write; artifact discarded")
	}
	check, err := openArtifactLeaf(s.workDir, false)
	if err != nil {
		removeOwn()
		return "", fmt.Errorf("verify bash artifact path: %w", err)
	}
	defer unix.Close(check) //nolint:errcheck // read-only descriptor close
	var cs unix.Stat_t
	if err := unix.Fstat(check, &cs); err != nil {
		removeOwn()
		return "", fmt.Errorf("verify bash artifact path: %w", err)
	}
	if cs.Dev != leafStat.Dev || cs.Ino != leafStat.Ino {
		removeOwn()
		return "", errors.New("bash artifact directory was replaced during write; artifact discarded")
	}
	if err := f.Close(); err != nil {
		removeOwn()
		return "", fmt.Errorf("close bash artifact: %w", err)
	}
	return path.Join(path.Join(bashArtifactDirs...), name), nil
}

// sameRegularFile reports whether name in dir (not following symlinks) is a
// regular file with the same device and inode as want. A small window remains
// between this check and a following unlinkat; it only matters to cleanup.
func sameRegularFile(dir int, name string, want *unix.Stat_t) bool {
	var got unix.Stat_t
	if err := unix.Fstatat(dir, name, &got, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}
	return got.Mode&unix.S_IFMT == unix.S_IFREG && got.Dev == want.Dev && got.Ino == want.Ino
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
