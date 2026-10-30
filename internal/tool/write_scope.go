package tool

import (
	"path/filepath"
)

// WriteScope restricts writable tool paths to files whose final name matches
// one of the scope's rules. Extensions are compared exactly against the final
// component's extension, leading dot included; Basenames are compared exactly
// against the whole final component. Matching is case-sensitive, directory
// components never grant eligibility, and existing directories are never
// eligible. The zero WriteScope places no restriction on writes.
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
