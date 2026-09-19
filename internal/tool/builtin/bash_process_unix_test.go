//go:build unix

package builtin

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBashSessionCloseKillsBackgroundChildren(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = s.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _, _, err := s.Execute(ctx, "sleep 300 >/dev/null 2>&1 &\necho $!")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("parse child pid from %q: %v", out, err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("background child not running before close: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Signal 0 succeeds on a zombie until reaped; reparented orphans get reaped by init.
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("background child %d survived session close", pid)
}
