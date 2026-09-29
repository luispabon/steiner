package builtin

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBashSessionStdinIsDevNull(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	run := func(cmd string) (string, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		start := time.Now()
		out, _, code, err := s.Execute(ctx, cmd)
		if err != nil {
			t.Fatalf("Execute(%q): %v", cmd, err)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatalf("Execute(%q) was not prompt", cmd)
		}
		return out, code
	}

	tests := []struct {
		name, cmd, want string
		wantCode        int
	}{
		{"cat", "cat", "", 0},
		{"read hits EOF", `read x; echo "got:$x"`, "got:", 0},
		{"heredoc", "cat <<EOF\nhi\nEOF", "hi", 0},
		{"explicit redirect", "echo data > in.$$; cat < in.$$; rm in.$$", "data", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, code := run(tc.cmd)
			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d", code, tc.wantCode)
			}
			if strings.TrimSpace(out) != tc.want {
				t.Errorf("stdout = %q, want %q", out, tc.want)
			}
		})
	}

	t.Run("state persists after stdin reader", func(t *testing.T) {
		dir := t.TempDir()
		run("cd " + dir)
		out, _ := run("pwd")
		if !strings.Contains(out, dir) {
			t.Errorf("pwd = %q, want to contain %q", out, dir)
		}
	})
}
