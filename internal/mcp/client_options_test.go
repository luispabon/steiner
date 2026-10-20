package mcp

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"testing"
)

// TestConnectSessionZeroValueOptions checks that a zero-value SessionOptions
// gives the same session as spelling out the defaults: DefaultConnectTimeout
// applied to the session and the fixture's tools listed. The session must also
// store the Release hook and Stderr writer it was given, because reconnects
// reuse both.
func TestConnectSessionZeroValueOptions(t *testing.T) {
	fixtureBin := buildFixture(t)
	spec := ServerSpec{Name: "fixture", Command: fixtureBin}

	var stderr bytes.Buffer
	var releaseCalls int
	release := func(*exec.Cmd) { releaseCalls++ }

	tests := []struct {
		name        string
		opts        SessionOptions
		wantStderr  io.Writer
		wantRelease bool
	}{
		{name: "zero value", opts: SessionOptions{}},
		{name: "explicit defaults", opts: SessionOptions{Stderr: io.Discard, Timeout: DefaultConnectTimeout}, wantStderr: io.Discard},
		{name: "release and distinct stderr", opts: SessionOptions{Release: release, Stderr: &stderr}, wantStderr: &stderr, wantRelease: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, err := ConnectSession(context.Background(), spec, nil, tt.opts)
			if err != nil {
				t.Fatalf("ConnectSession() error = %v", err)
			}
			defer sess.Close() //nolint:errcheck

			if sess.timeout != DefaultConnectTimeout {
				t.Errorf("timeout = %v, want %v", sess.timeout, DefaultConnectTimeout)
			}
			if got := len(sess.Tools()); got != 6 {
				t.Errorf("Tools() has %d tools, want 6", got)
			}
			if sess.stderr != tt.wantStderr {
				t.Errorf("stderr = %v, want %v", sess.stderr, tt.wantStderr)
			}
			if (sess.release != nil) != tt.wantRelease {
				t.Fatalf("release set = %t, want %t", sess.release != nil, tt.wantRelease)
			}
			if tt.wantRelease {
				before := releaseCalls
				sess.release(&exec.Cmd{})
				if releaseCalls != before+1 {
					t.Error("stored release did not call the Release func that was passed in")
				}
			}
		})
	}
}
