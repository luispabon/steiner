//go:build unix

package tool

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestPolicy_ResolveReadEntryPath_AllowsSpecialFilesButHonorsBlockedPaths
// proves listing a special file is allowed while reading it is denied, and
// that blocked paths still deny both.
func TestPolicy_ResolveReadEntryPath_AllowsSpecialFilesButHonorsBlockedPaths(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("Mkfifo unsupported: %v", err)
	}
	blockedDir := filepath.Join(root, "secrets")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("mkdir blocked dir: %v", err)
	}
	blockedFifo := filepath.Join(blockedDir, "pipe")
	if err := syscall.Mkfifo(blockedFifo, 0o600); err != nil {
		t.Skipf("Mkfifo unsupported: %v", err)
	}

	policy := NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})

	if _, err := policy.ResolveReadPath(fifo); err == nil {
		t.Fatalf("ResolveReadPath(%q) = nil error, want special-file denial", fifo)
	}
	if _, err := policy.ResolveReadEntryPath(fifo); err != nil {
		t.Fatalf("ResolveReadEntryPath(%q) error = %v, want nil", fifo, err)
	}
	if _, err := policy.ResolveReadEntryPath(blockedFifo); err == nil {
		t.Fatalf("ResolveReadEntryPath(%q) = nil error, want blocked-path denial", blockedFifo)
	}
}
