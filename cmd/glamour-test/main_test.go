package main

import (
	"errors"
	"strings"
	"testing"
)

type failing_writer struct {
	err error
}

func (w failing_writer) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWriteOutput(t *testing.T) {
	t.Run("writes output", func(t *testing.T) {
		var w strings.Builder

		if err := write_output(&w, "hello\n"); err != nil {
			t.Fatalf("write_output() error = %v", err)
		}

		if got := w.String(); got != "hello\n" {
			t.Fatalf("write_output() wrote %q, want %q", got, "hello\n")
		}
	})

	t.Run("returns write error", func(t *testing.T) {
		wantErr := errors.New("broken pipe")

		err := write_output(failing_writer{err: wantErr}, "hello\n")
		if !errors.Is(err, wantErr) {
			t.Fatalf("write_output() error = %v, want %v", err, wantErr)
		}
	})
}
