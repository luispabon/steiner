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

	"golang.org/x/sys/windows"
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
	reader := bufio.NewReader(os.Stdin)
	command, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(command) != "release" {
		t.Fatalf("helper command = %q, %v", command, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(os.Stdout, "released\n"); err != nil {
		t.Fatal(err)
	}
	command, err = reader.ReadString('\n')
	if err != nil || strings.TrimSpace(command) != "exit" {
		t.Fatalf("helper command = %q, %v", command, err)
	}
}

func TestWindowsUnsupportedLockClosesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	var handle windows.Handle
	lock, err := acquireWindows(context.Background(), path, func(h windows.Handle, _, _, _, _ uint32, _ *windows.Overlapped) error {
		handle = h
		return windows.ERROR_NOT_SUPPORTED
	})
	if err != nil {
		t.Fatalf("acquireWindows() error = %v", err)
	}
	if !lock.Unsupported() {
		t.Fatal("Unsupported() = false")
	}
	if !strings.Contains(lock.Warning(), path) || !strings.Contains(lock.Warning(), windows.ERROR_NOT_SUPPORTED.Error()) {
		t.Errorf("Warning() = %q, want path and unsupported cause", lock.Warning())
	}
	if _, err := windows.GetFileType(handle); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Errorf("handle remains open after unsupported result: GetFileType() error = %v", err)
	}
}

func startWindowsHolder(t *testing.T, path string) (*exec.Cmd, io.WriteCloser, <-chan string, <-chan error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLockHelper$")
	cmd.Env = append(os.Environ(), helperEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = stdoutWriter
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		t.Fatal(err)
	}
	if err := stdoutWriter.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = stdout.Close()
		t.Fatal(err)
	}
	lines := make(chan string, 4)
	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			lines <- line
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = stdin.Close()
		_ = stdout.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("holder process did not exit")
		}
	})
	select {
	case line := <-lines:
		if strings.TrimSpace(line) != "locked" {
			t.Fatalf("holder readiness = %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("holder did not acquire lock")
	}
	return cmd, stdin, lines, done
}

func TestWindowsSubprocessExclusionCancellationHandoffAndTermination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	_, stdin, lines, done := startWindowsHolder(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if lock, err := Acquire(ctx, path); err == nil {
		_ = lock.Release()
		t.Fatal("Acquire() passed while subprocess held lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire() error = %v, want deadline exceeded", err)
	}
	waiting := make(chan error, 1)
	contended := make(chan struct{}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		lock, err := acquireWindows(ctx, path, func(h windows.Handle, flags, reserved, low, high uint32, overlapped *windows.Overlapped) error {
			err := windows.LockFileEx(h, flags, reserved, low, high, overlapped)
			if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
				select {
				case contended <- struct{}{}:
				default:
				}
			}
			return err
		})
		if err == nil {
			err = lock.Release()
		}
		waiting <- err
	}()
	select {
	case <-contended:
	case <-time.After(5 * time.Second):
		t.Fatal("contender did not observe lock contention")
	}
	if _, err := io.WriteString(stdin, "release\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-lines:
		if strings.TrimSpace(line) != "released" {
			t.Fatalf("holder output = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("holder did not release lock")
	}
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatalf("Acquire() after live holder release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("contender did not acquire after holder released while still alive")
	}
	if _, err := io.WriteString(stdin, "exit\n"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("holder exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("holder process did not finish")
	}

	terminated, termIn, _, termDone := startWindowsHolder(t, path)
	if err := terminated.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = termIn.Close()
	select {
	case err := <-termDone:
		if err == nil {
			t.Fatal("terminated holder exited without error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminated holder did not exit")
	}
	reacquireCtx, reacquireCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reacquireCancel()
	after, err := Acquire(reacquireCtx, path)
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
