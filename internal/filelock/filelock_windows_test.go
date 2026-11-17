//go:build windows

package filelock

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const helperEnv = "STEINER_FILELOCK_HELPER"

func TestWindowsLockHelper(t *testing.T) {
	path := os.Getenv(helperEnv)
	if path == "" {
		return
	}
	lock, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if lock.Unsupported() {
		t.Fatal("Acquire() returned unsupported lock")
	}
	if _, err := io.WriteString(os.Stdout, "locked\n"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSubprocessExclusionCancellationAndTermination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLockHelper$")
	cmd.Env = append(os.Environ(), helperEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "locked" {
			t.Fatalf("holder output = %q, want locked", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("holder did not acquire lock")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if lock, err := Acquire(ctx, path); err == nil {
		_ = lock.Release()
		t.Fatal("Acquire() passed while subprocess held lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire() error = %v, want deadline exceeded", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		// Termination is expected. The OS releases the kernel lock with the handle.
	}
	after, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() after holder termination: %v", err)
	}
	if err := after.Release(); err != nil {
		t.Fatalf("Release(): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent lock file missing: %v", err)
	}
}
