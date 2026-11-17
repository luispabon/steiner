package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	projectDirName       = ".steiner"
	projectGitignoreName = ".gitignore"
	projectGitignoreBody = "*\n"
)

// EnsureSteinerProjectDir creates the .steiner directory and a .gitignore file.
// It is idempotent and safe to call concurrently, including from separate
// processes.
//
// workDir must already exist; the helper does not create it and returns an
// error naming the path when it is missing.
//
// Every step is anchored to workDir through os.OpenRoot, so the helper never
// writes through a symlink out of the project: a symlinked or non-directory
// .steiner is rejected instead of followed, and .gitignore is created
// exclusively so an existing symlink, dangling or not, is never traversed. The
// workDir path itself may be a symlink; it is resolved once when the root is
// opened.
func EnsureSteinerProjectDir(workDir string) error {
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return fmt.Errorf("open project directory %s to create %s: %w", workDir, projectDirName, err)
	}
	defer func() {
		// The root is a read-only anchor; a close failure leaves nothing to repair.
		_ = root.Close()
	}()

	dir, err := openProjectDir(root, workDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = dir.Close()
	}()

	return ensureProjectGitignore(dir, workDir)
}

// openProjectDir creates .steiner when missing and returns it as an anchored
// root. It rejects a symlinked or non-directory .steiner, and verifies that the
// directory it opened is the one it inspected so a concurrent swap cannot
// redirect the .gitignore write.
func openProjectDir(root *os.Root, workDir string) (*os.Root, error) {
	if err := root.Mkdir(projectDirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create %s directory in %s: %w", projectDirName, workDir, err)
	}

	info, err := root.Lstat(projectDirName)
	if err != nil {
		return nil, fmt.Errorf("inspect %s in %s: %w", projectDirName, workDir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s in %s is a symlink; replace it with a real directory", projectDirName, workDir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s in %s is not a directory; move it aside so steiner can create its project directory", projectDirName, workDir)
	}

	dir, err := root.OpenRoot(projectDirName)
	if err != nil {
		return nil, fmt.Errorf("open %s in %s: %w", projectDirName, workDir, err)
	}
	opened, err := dir.Stat(".")
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("inspect opened %s in %s: %w", projectDirName, workDir, err)
	}
	if err := verifyProjectDirUnchanged(info, opened, workDir); err != nil {
		_ = dir.Close()
		return nil, err
	}
	return dir, nil
}

// verifyProjectDirUnchanged rejects a directory that was swapped after it was
// inspected, so the .gitignore write lands in the directory that passed the
// symlink check.
func verifyProjectDirUnchanged(inspected, opened fs.FileInfo, workDir string) error {
	if os.SameFile(inspected, opened) {
		return nil
	}
	return fmt.Errorf("%s in %s was replaced while steiner set it up; retry once nothing else is modifying it", projectDirName, workDir)
}

// ensureProjectGitignore exclusively creates .gitignore when missing. A file
// that already exists must be regular, and its contents are left untouched.
func ensureProjectGitignore(dir *os.Root, workDir string) error {
	path := filepath.Join(workDir, projectDirName, projectGitignoreName)

	file, err := dir.OpenFile(projectGitignoreName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		if _, err := file.WriteString(projectGitignoreBody); err != nil {
			_ = file.Close()
			return fmt.Errorf("write %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create %s: %w", path, err)
	}

	info, err := dir.Lstat(projectGitignoreName)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink; replace it with a regular file", path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file; replace it with a regular file", path)
	}
	return nil
}
