package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
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
		{"oversize", strings.Repeat("x", maxReadFileBytes+1), "file too large (max 1.0mb"},
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

func TestReadRangeAcceptsFileAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	line := strings.Repeat("x", 1023) + "\n"
	body := strings.Repeat(line, maxReadFileBytes/len(line))
	if err := os.WriteFile(filepath.Join(dir, "limit.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{{"path": "limit.txt"}, {"path": "limit.txt", "offset": 2, "limit": 1}} {
		got, err := def.Handler(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		result, ok := got.(ReadResult)
		if !ok {
			t.Fatalf("result type %T, want ReadResult", got)
		}
		if result.Path == "" || result.TotalLines != 1024 || strings.Contains(result.Output, "too large") {
			t.Fatalf("args %v: result = %+v, want a successful read of 1024 lines", args, result)
		}
	}
}

func TestReadRangePagesLargeFileWithWholeFileMetadata(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	lines := make([]string, 4000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%05d %s\n", i+1, strings.Repeat("x", 40))
	}
	data := []byte(strings.Join(lines, ""))
	if len(data) <= 100*1024 || len(data) >= maxReadFileBytes {
		t.Fatalf("fixture size = %d, want between 100KB and 1MB", len(data))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := def.Handler(context.Background(), map[string]any{"path": "big.txt", "offset": 2000, "limit": 3})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := got.(ReadResult)
	if !ok {
		t.Fatalf("result type %T, want ReadResult", got)
	}
	if want := strings.Join(lines[1999:2002], ""); result.Output != want {
		t.Errorf("output = %q, want %q", result.Output, want)
	}
	if result.FileHash != FileContentHash(data) {
		t.Errorf("file_hash = %q, want whole-file hash %q", result.FileHash, FileContentHash(data))
	}
	if result.TotalLines != 4000 || result.StartLine != 2000 || result.EndLine != 2002 || result.NextOffset != 2003 {
		t.Errorf("range = start %d end %d next %d total %d, want 2000 2002 2003 4000", result.StartLine, result.EndLine, result.NextOffset, result.TotalLines)
	}
}

func TestReadUpToReportsExceededLimit(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name         string
		input        string
		wantExceeded bool
		wantLen      int
	}{
		{name: "empty", input: "", wantExceeded: false, wantLen: 0},
		{name: "exactly limit", input: strings.Repeat("x", 10), wantExceeded: false, wantLen: 10},
		{name: "one byte over limit", input: strings.Repeat("x", 11), wantExceeded: true, wantLen: 11},
		{name: "far over limit is capped", input: strings.Repeat("x", 100), wantExceeded: true, wantLen: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, exceeded, err := readUpTo(strings.NewReader(tc.input), 10)
			if err != nil {
				t.Fatal(err)
			}
			if exceeded != tc.wantExceeded || len(data) != tc.wantLen {
				t.Errorf("readUpTo = (len %d, exceeded %t), want (len %d, exceeded %t)", len(data), exceeded, tc.wantLen, tc.wantExceeded)
			}
		})
	}
	if _, _, err := readUpTo(iotest.ErrReader(boom), 10); !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestReadRangeOversizeMessageNamesLimit(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("x", maxReadFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := def.Handler(context.Background(), map[string]any{"path": "big.txt", "offset": 2, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := got.(*ReadResult)
	if !ok {
		t.Fatalf("result type %T, want *ReadResult", got)
	}
	want := "Error: file too large (max 1.0MB, got 1.0MB); offset/limit cannot bypass this limit, use grep or bash to inspect it"
	if result.Output != want {
		t.Errorf("output = %q, want %q", result.Output, want)
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
