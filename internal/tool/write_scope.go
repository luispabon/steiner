package tool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteScope restricts writable tool paths to files whose final name matches
// one of the scope's rules. Extensions are compared exactly against the final
// component's extension, leading dot included; Basenames are compared exactly
// against the whole final component. Matching is case-sensitive, directory
// components never grant eligibility, a final component with an empty stem
// after trimming dots (for example ".md") never matches an extension rule, and
// existing directories are never eligible. The zero WriteScope places no
// restriction on writes.
type WriteScope struct {
	// Reason is the message reported when a write falls outside the scope.
	Reason string
	// Extensions lists allowed file extensions, leading dot included (for
	// example ".md").
	Extensions []string
	// Basenames lists allowed extensionless file names (for example
	// "README").
	Basenames []string
}

// cloneWriteScope returns a copy of scope with freshly allocated collections.
func cloneWriteScope(scope WriteScope) WriteScope {
	return WriteScope{
		Reason:     scope.Reason,
		Extensions: append([]string(nil), scope.Extensions...),
		Basenames:  append([]string(nil), scope.Basenames...),
	}
}

// active reports whether scope restricts anything. A scope with no rules
// leaves writes unrestricted.
func (s WriteScope) active() bool {
	return len(s.Extensions) > 0 || len(s.Basenames) > 0
}

// allows reports whether the final component of path matches one of the
// scope's rules. Directory components are ignored.
func (s WriteScope) allows(path string) bool {
	name := filepath.Base(filepath.Clean(path))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return false
	}
	for _, basename := range s.Basenames {
		if name == basename {
			return true
		}
	}
	ext := filepath.Ext(name)
	if ext == "" {
		return false
	}
	// Hidden dotfiles such as ".md" and "..md" have an extension but no stem;
	// they are not files the scope can name, so an extension rule never matches.
	if strings.Trim(strings.TrimSuffix(name, ext), ".") == "" {
		return false
	}
	for _, extension := range s.Extensions {
		if ext == extension {
			return true
		}
	}
	return false
}

// denialReason returns the scope's configured denial message, falling back to
// a generic one when the caller supplied none.
func (s WriteScope) denialReason() string {
	if s.Reason != "" {
		return s.Reason
	}
	return "writes are restricted by tool policy to an allowlist of file names"
}

// denial builds the non-promptable denial for path. The reason keeps the
// scope's configured text as a prefix and names the offending path.
func (s WriteScope) denial(path string) *PathPolicyError {
	return &PathPolicyError{
		Path:       path,
		Reason:     fmt.Sprintf("%s: path %q is outside the write scope", s.denialReason(), path),
		Promptable: false,
	}
}

// ensureWriteScopeAllowed rejects writable paths outside the active write
// scope. The path must resolve inside the scope root, both the lexical path and
// its symlink-resolved form must match the scope's name rules, and an existing
// directory is never eligible even when its name matches. The check fails
// closed: a scope installed without a root denies every write, a stat failure
// denies, and the denial is never promptable.
func (p PathPolicy) ensureWriteScopeAllowed(path string, writable bool) error {
	if !writable || !p.writeScope.active() {
		return nil
	}
	denial := p.writeScope.denial(path)
	if p.writeScopeRoot == "" {
		return denial
	}
	resolvedRoot, err := resolvePathWithMissing(p.writeScopeRoot)
	if err != nil {
		return p.policyResolutionError(path, fmt.Errorf("resolve write scope root: %w", err))
	}
	resolvedPath, err := resolvePathWithMissing(path)
	if err != nil {
		return p.policyResolutionError(path, fmt.Errorf("resolve write scope path: %w", err))
	}
	// Containment is checked against the resolved root rather than the lexical
	// one: the scope root may itself sit behind a symlink, and the mutate planner
	// re-checks canonical link targets through this same policy. A symlinked
	// directory component that escapes the resolved root is still denied.
	if !pathWithinRoot(resolvedRoot, resolvedPath) {
		return denial
	}
	info, statErr := os.Stat(resolvedPath)
	if statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			return p.policyResolutionError(path, fmt.Errorf("stat write scope path: %w", statErr))
		}
	} else if info.IsDir() {
		// Directories are never eligible, even when their name matches a rule:
		// deleting or moving a directory would escape the file-level scope.
		return denial
	}
	if !p.writeScope.allows(path) || !p.writeScope.allows(resolvedPath) {
		return denial
	}
	return nil
}
