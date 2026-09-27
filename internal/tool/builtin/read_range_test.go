package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestReadTextRangeReturnsPageAndMetadataFromReadBytes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		start     int
		limit     int
		want      string
		wantLines int
	}{
		{name: "selected lines", body: "one\ntwo\nthree\n", start: 2, limit: 1, want: "two\n", wantLines: 3},
		{name: "no trailing newline", body: "one\ntwo", start: 1, limit: 2, want: "one\ntwo", wantLines: 2},
		{name: "empty file", body: "", start: 1, limit: 2, want: "", wantLines: 0},
		{name: "LF", body: "one\ntwo", start: 1, limit: 2, want: "one\ntwo", wantLines: 2},
		{name: "CRLF", body: "one\r\ntwo\r\n", start: 1, limit: 2, want: "one\r\ntwo\r\n", wantLines: 2},
		{name: "trailing newline", body: "one\n", start: 1, limit: 2, want: "one\n", wantLines: 1},
		{name: "trailing whitespace including carriage return, spaces, and tabs", body: "one\r \t", start: 1, limit: 2, want: "one\r \t", wantLines: 1},
		{name: "newline only", body: "\n", start: 1, limit: 2, want: "\n", wantLines: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.txt")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			gotHash, gotLines, page, err := readTextRange(path, tc.start, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(page, ""); got != tc.want {
				t.Errorf("page = %q, want %q", got, tc.want)
			}
			wantHash := FileContentHash([]byte(tc.body))
			if gotHash != wantHash || gotLines != tc.wantLines {
				t.Errorf("metadata = (%q, %d), want (%q, %d)", gotHash, gotLines, wantHash, tc.wantLines)
			}
		})
	}
}

func TestReadRangeRejectsBinaryAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	cases := []struct{ name, body, want string }{
		{"binary", "\x00binary", "binary"},
		{"oversize", strings.Repeat("x", 100*1024+1), "too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, args := range []map[string]any{{"path": "input.txt"}, {"path": "input.txt", "offset": 2, "limit": 1}} {
				got, err := def.Handler(context.Background(), args)
				if err != nil {
					t.Fatal(err)
				}
				result, ok := got.(*ReadResult)
				if !ok {
					t.Fatalf("result type %T, want *ReadResult", got)
				}
				if !strings.Contains(strings.ToLower(result.Output), tc.want) {
					t.Fatalf("output = %q, want %q", result.Output, tc.want)
				}
			}
		})
	}
}

func TestReadImageRouteAndSpecialFileProtection(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("not-decoded but image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := def.Handler(context.Background(), map[string]any{"path": "pic.png"})
	if err != nil {
		t.Fatal(err)
	}
	if result, ok := got.(*ReadResult); !ok || result.Image == nil {
		t.Fatalf("image route result = %#v", got)
	}
	fifo := filepath.Join(dir, "pipe.txt")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("Mkfifo unsupported: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := def.Handler(context.Background(), map[string]any{"path": "pipe.txt", "offset": 2})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("special file accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("special file read blocked")
	}
}
