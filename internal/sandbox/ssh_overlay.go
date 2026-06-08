//go:build linux

package sandbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	sshSystemConfigPath = "/etc/ssh/ssh_config"
	sshMaxIncludeDepth  = 8
	sshMaxFiles         = 128
	sshMaxFileBytes     = 1 << 20
	sshMaxTotalBytes    = 8 << 20
)

// sshOverlay owns the generated bwrap args and any memfd-backed files.
type sshOverlay struct {
	bwrapArgs []string
	memfds    []*os.File
}

// Close releases any open memfd files owned by the overlay.
func (o *sshOverlay) Close() error {
	if o == nil {
		return nil
	}

	var errs []error
	for _, f := range o.memfds {
		if f == nil {
			continue
		}
		if err := f.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ssh overlay memfd: %w", err))
		}
	}

	o.memfds = nil

	return errors.Join(errs...)
}

// sshOverlayFile describes a resolved SSH file in the sandbox.
type sshOverlayFile struct {
	sourcePath  string
	sandboxPath string
	content     []byte
	memfd       *os.File
}

// sshIncludeResolution is the result of resolving SSH include directives.
type sshIncludeResolution struct {
	files              []sshOverlayFile
	replacementDirs    []string
	skippedDiagnostics []string
}

type sshResolveState struct {
	baseDir            string
	files              []sshOverlayFile
	replacementDirs    []string
	skippedDiagnostics []string
	seenSources        map[string]struct{}
	seenSandboxPaths   map[string]struct{}
	parsedSources      map[string]struct{}
	replacementDirSet  map[string]struct{}
	totalBytes         int
}

func prepareSSHOverlayFromPath(rootPath string, childFDBase int) (*sshOverlay, error) {
	resolution := resolveSSHIncludeResolution(rootPath)
	if len(resolution.files) == 0 {
		return nil, nil
	}

	overlay := &sshOverlay{
		memfds: make([]*os.File, 0, len(resolution.files)),
	}

	for i := range resolution.files {
		memfd, err := createMemfdFromContent(resolution.files[i].sandboxPath, resolution.files[i].content)
		if err != nil {
			return overlay, fmt.Errorf("create ssh overlay memfd for %s: %w", resolution.files[i].sandboxPath, err)
		}
		resolution.files[i].memfd = memfd
		overlay.memfds = append(overlay.memfds, memfd)
	}

	overlay.bwrapArgs = buildSSHOverlayArgs(resolution, childFDBase)
	return overlay, nil
}

func resolveSSHIncludeResolution(rootPath string) sshIncludeResolution {
	state := &sshResolveState{
		baseDir:           filepath.Dir(rootPath),
		seenSources:       make(map[string]struct{}),
		seenSandboxPaths:  make(map[string]struct{}),
		parsedSources:     make(map[string]struct{}),
		replacementDirSet: make(map[string]struct{}),
	}

	state.addFile(rootPath, rootPath, 0)

	return sshIncludeResolution{
		files:              state.files,
		replacementDirs:    state.replacementDirs,
		skippedDiagnostics: state.skippedDiagnostics,
	}
}

func (s *sshResolveState) addFile(candidatePath, sandboxPath string, depth int) {
	if len(s.files) >= sshMaxFiles {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: file limit reached", sandboxPath))
		return
	}

	resolvedPath, err := filepath.EvalSymlinks(candidatePath)
	if err != nil {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: resolve symlink: %v", sandboxPath, err))
		return
	}

	if _, ok := s.seenSources[resolvedPath]; ok {
		return
	}
	if _, ok := s.seenSandboxPaths[sandboxPath]; ok {
		return
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: stat file: %v", sandboxPath, err))
		return
	}
	if !info.Mode().IsRegular() {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: not a regular file", sandboxPath))
		return
	}
	if info.Size() > sshMaxFileBytes {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: file too large", sandboxPath))
		return
	}
	if s.totalBytes+int(info.Size()) > sshMaxTotalBytes {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: total byte limit reached", sandboxPath))
		return
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: read file: %v", sandboxPath, err))
		return
	}

	s.files = append(s.files, sshOverlayFile{
		sourcePath:  resolvedPath,
		sandboxPath: sandboxPath,
		content:     content,
	})
	s.seenSources[resolvedPath] = struct{}{}
	s.seenSandboxPaths[sandboxPath] = struct{}{}
	s.totalBytes += len(content)
	s.addReplacementDir(filepath.Dir(sandboxPath))

	if depth >= sshMaxIncludeDepth {
		return
	}
	if _, parsed := s.parsedSources[resolvedPath]; parsed {
		return
	}
	s.parsedSources[resolvedPath] = struct{}{}

	for _, includePath := range parseSSHIncludePaths(content) {
		s.addStaticInclude(includePath, depth+1)
	}
}

func (s *sshResolveState) addStaticInclude(includePath string, depth int) {
	if !isStaticSSHInclude(includePath) {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: dynamic path", includePath))
		return
	}

	matches, err := expandSSHInclude(includePath)
	if err != nil {
		s.skippedDiagnostics = append(s.skippedDiagnostics, fmt.Sprintf("skip ssh include %s: expand glob: %v", includePath, err))
		return
	}
	if len(matches) == 0 {
		return
	}

	for _, match := range matches {
		s.addFile(match, match, depth)
	}
}

func (s *sshResolveState) addReplacementDir(dir string) {
	if dir == "" || dir == "." || dir == s.baseDir {
		return
	}

	basePrefix := s.baseDir + string(os.PathSeparator)
	if !strings.HasPrefix(dir, basePrefix) {
		return
	}
	if _, ok := s.replacementDirSet[dir]; ok {
		return
	}

	s.replacementDirSet[dir] = struct{}{}
	s.replacementDirs = append(s.replacementDirs, dir)
}

func parseSSHIncludePaths(content []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 4096), sshMaxFileBytes)

	var includes []string
	for scanner.Scan() {
		fields, ok := splitSSHConfigFields(scanner.Text())
		if !ok || len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
			continue
		}
		includes = append(includes, fields[1:]...)
	}

	return includes
}

func splitSSHConfigFields(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, true
	}

	var (
		fields   []string
		current  strings.Builder
		inQuote  bool
		escaped  bool
		hasToken bool
	)

	flush := func() {
		if !hasToken {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
		hasToken = false
	}

	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]

		if escaped {
			current.WriteByte(ch)
			hasToken = true
			escaped = false
			continue
		}

		switch ch {
		case '\\':
			escaped = true
		case '"':
			inQuote = !inQuote
			hasToken = true
		case '#':
			if inQuote {
				current.WriteByte(ch)
				hasToken = true
				continue
			}
			flush()
			return fields, true
		case ' ', '\t', '\r':
			if inQuote {
				current.WriteByte(ch)
				hasToken = true
				continue
			}
			flush()
		default:
			current.WriteByte(ch)
			hasToken = true
		}
	}

	if escaped || inQuote {
		return nil, false
	}

	flush()
	return fields, true
}

func isStaticSSHInclude(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	if strings.Contains(path, "%") || strings.Contains(path, "$") || strings.Contains(path, "~") {
		return false
	}
	return true
}

func expandSSHInclude(path string) ([]string, error) {
	if hasGlobMeta(path) {
		return filepath.Glob(path)
	}
	return []string{path}, nil
}

func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func createMemfdFromContent(name string, content []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate(filepath.Base(name), unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("wrap memfd")
	}

	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}

	return file, nil
}

func buildSSHOverlayArgs(resolution sshIncludeResolution, childFDBase int) []string {
	args := make([]string, 0, len(resolution.replacementDirs)*2+len(resolution.files)*4)

	for _, dir := range resolution.replacementDirs {
		args = append(args, "--tmpfs", dir)
	}
	for i, file := range resolution.files {
		args = append(args,
			"--perms", "0644",
			"--ro-bind-data", strconv.Itoa(childFDBase+i), file.sandboxPath,
		)
	}

	return args
}
