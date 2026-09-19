package builtin

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"
)

func TestBashSessionOutputWithoutTrailingNewline(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantStdout string
		wantStderr string
		wantCode   int
		truncated  bool
	}{
		{name: "stdout no newline", command: "printf foo", wantStdout: "foo"},
		{name: "stderr no newline", command: "printf foo >&2", wantStderr: "foo"},
		{name: "both no newline", command: "printf a; printf b >&2", wantStdout: "a", wantStderr: "b"},
		{name: "exit code after no newline", command: "printf foo; (exit 3)", wantStdout: "foo", wantCode: 3},
		{name: "trailing newline unchanged", command: "echo foo", wantStdout: "foo\n"},
		{name: "two trailing newlines", command: "printf 'foo\\n\\n'", wantStdout: "foo\n\n"},
		{name: "empty output", command: "true"},
		{name: "oversized no newline", command: "head -c 300000 /dev/zero | tr '\\0' a", wantStdout: strings.Repeat("a", bashSessionMaxOutput) + "\n[output truncated]", truncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewBashSession()
			if err := s.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = s.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stdout, stderr, code, trunc, err := s.execute(ctx, tt.command)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if stdout != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", clip(stdout), clip(tt.wantStdout))
			}
			if stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if trunc != tt.truncated {
				t.Errorf("truncated = %v, want %v", trunc, tt.truncated)
			}

			stdout, _, code, _, err = s.execute(ctx, "echo next")
			if err != nil || code != 0 || stdout != "next\n" {
				t.Errorf("follow-up = %q, %d, %v; want \"next\\n\", 0, nil", stdout, code, err)
			}
		})
	}
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:20] + "..." + s[len(s)-20:]
	}
	return s
}

func TestReadUntilMarkerSuffixMarker(t *testing.T) {
	const marker = "__M__"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"same line", "foo" + marker + "\n", "foo"},
		{"after lines", "a\nb" + marker + "\n", "a\nb"},
		{"marker only", marker + "\n", ""},
		{"long line then marker", strings.Repeat("x", 9000) + marker + "\n", strings.Repeat("x", 9000)},
		{"partial marker text kept", "__M_" + "\n" + marker + "\n", "__M_\n"},
	}
	for _, tt := range tests {
		for _, size := range []int{16, 4096} {
			t.Run(tt.name, func(t *testing.T) {
				r := bufio.NewReaderSize(strings.NewReader(tt.input), size)
				got, trunc, err := readUntilMarker(r, marker, 1<<20)
				if err != nil || trunc || got != tt.want {
					t.Errorf("got %q, %v, %v; want %q", got, trunc, err, tt.want)
				}
			})
		}
	}
}
