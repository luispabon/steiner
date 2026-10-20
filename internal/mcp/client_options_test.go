package mcp

import (
	"context"
	"io"
	"testing"
)

// TestConnectSessionZeroValueOptions checks that a zero-value SessionOptions
// gives the same session as spelling out the defaults: DefaultConnectTimeout
// applied to the session and the fixture's tools listed.
func TestConnectSessionZeroValueOptions(t *testing.T) {
	fixtureBin := buildFixture(t)
	spec := ServerSpec{Name: "fixture", Command: fixtureBin}

	tests := []struct {
		name string
		opts SessionOptions
	}{
		{name: "zero value", opts: SessionOptions{}},
		{name: "explicit defaults", opts: SessionOptions{Stderr: io.Discard, Timeout: DefaultConnectTimeout}},
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
		})
	}
}
