package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestReadTool(t *testing.T) {
	tmpDir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewReadTool(env)
	ctx := context.Background()

	t.Run("returns requested line slice", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 2,
			"limit":  3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if result.StartLine != 2 {
			t.Errorf("StartLine = %d, want 2", result.StartLine)
		}
		if result.EndLine != 4 {
			t.Errorf("EndLine = %d, want 4", result.EndLine)
		}
		if result.TotalLines != 10 {
			t.Errorf("TotalLines = %d, want 10", result.TotalLines)
		}
	})

	t.Run("includes total_lines and next_offset when not at end", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 5,
			"limit":  3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if result.TotalLines != 10 {
			t.Errorf("TotalLines = %d, want 10", result.TotalLines)
		}
		if result.NextOffset != 8 {
			t.Errorf("NextOffset = %d, want 8 (5+3)", result.NextOffset)
		}
	})

	t.Run("no next_offset when reading to end", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 1,
			"limit":  50,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if result.NextOffset != 0 {
			t.Errorf("NextOffset = %d, want 0", result.NextOffset)
		}
	})

	t.Run("handles file not found", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "nonexistent.txt",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want *ReadResult", resultI)
		}
		if result.Output == "" {
			t.Error("expected error message in Output for nonexistent file")
		}
	})

	t.Run("includes file_hash for successful read", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "test.txt",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if result.FileHash == "" {
			t.Fatal("FileHash is empty, want non-empty hash")
		}
		expected := FileContentHash([]byte(content))
		if result.FileHash != expected {
			t.Errorf("FileHash = %q, want %q", result.FileHash, expected)
		}
	})

	t.Run("file_hash is stable across reads", func(t *testing.T) {
		r1, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 1,
			"limit":  3,
		})
		if err != nil {
			t.Fatalf("first read error: %v", err)
		}
		r2, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 5,
			"limit":  3,
		})
		if err != nil {
			t.Fatalf("second read error: %v", err)
		}
		h1 := r1.(ReadResult).FileHash
		h2 := r2.(ReadResult).FileHash
		if h1 != h2 {
			t.Errorf("hash mismatch: first read %q, second read %q", h1, h2)
		}
	})

	t.Run("file_hash is empty on error", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "nonexistent.txt",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want *ReadResult", resultI)
		}
		if result.FileHash != "" {
			t.Errorf("FileHash = %q, want empty for error result", result.FileHash)
		}
	})

	t.Run("empty file returns empty output", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tmpDir, "empty.txt"), []byte{}, 0o644); err != nil {
			t.Fatalf("write empty file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "empty.txt",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if result.TotalLines != 0 {
			t.Errorf("TotalLines = %d, want 0", result.TotalLines)
		}
		if result.Output != "" {
			t.Errorf("Output = %q, want empty", result.Output)
		}
	})

	t.Run("huge single line is truncated with marker", func(t *testing.T) {
		hugeLine := strings.Repeat("x", 2500) + "\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "huge.txt"), []byte(hugeLine), 0o644); err != nil {
			t.Fatalf("write huge file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":  "huge.txt",
			"limit": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if !strings.Contains(result.Output, "…<truncated>") {
			t.Errorf("Output = %q, want to contain …<truncated> marker", result.Output)
		}
		if result.StartLine != 1 {
			t.Errorf("StartLine = %d, want 1", result.StartLine)
		}
		if result.EndLine != 1 {
			t.Errorf("EndLine = %d, want 1", result.EndLine)
		}
		if result.TotalLines != 1 {
			t.Errorf("TotalLines = %d, want 1", result.TotalLines)
		}
	})

	t.Run("small line is not truncated", func(t *testing.T) {
		smallLine := strings.Repeat("z", 100) + "\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "small.txt"), []byte(smallLine), 0o644); err != nil {
			t.Fatalf("write small file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":  "small.txt",
			"limit": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if strings.Contains(result.Output, "…<truncated>") {
			t.Errorf("Output should not be truncated for short line, got: %s", result.Output)
		}
	})
	t.Run("exact-boundary line length is not truncated", func(t *testing.T) {
		// Dive adds a "%6d\t" prefix (7 runes for line 1) when offset/limit
		// Use 1990 content runes, genuinely under 2000 after the rendered prefix.
		exactLine := strings.Repeat("y", 1990) + "\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "exact.txt"), []byte(exactLine), 0o644); err != nil {
			t.Fatalf("write exact file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":  "exact.txt",
			"limit": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if strings.Contains(result.Output, "…<truncated>") {
			t.Errorf("Output should not be truncated for exact-boundary line, got: %s", result.Output)
		}
	})

	t.Run("paged read has next_offset", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 2,
			"limit":  3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(ReadResult)
		if !ok {
			t.Fatalf("result type = %T, want ReadResult", resultI)
		}
		if result.NextOffset != 5 {
			t.Errorf("NextOffset = %d, want 5", result.NextOffset)
		}
	})

	t.Run("long prose lines are preserved", func(t *testing.T) {
		lines := []string{strings.Repeat("a", 600), strings.Repeat("b", 600), strings.Repeat("c", 600)}
		if err := os.WriteFile(filepath.Join(tmpDir, "prose.md"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write prose file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{"path": "prose.md"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := resultI.(ReadResult)
		if strings.Contains(result.Output, "…<truncated>") {
			t.Errorf("prose read was line-truncated: output=%q", result.Output)
		}
	})

	t.Run("oversized paginated read rejected by size limit", func(t *testing.T) {
		content := strings.Repeat("x", 100*1024+1)
		path := filepath.Join(tmpDir, "paged-large.txt")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range []map[string]any{{"path": "paged-large.txt"}, {"path": "paged-large.txt", "offset": 2}} {
			got, err := toolDef.Handler(ctx, args)
			if err != nil {
				t.Fatal(err)
			}
			result := got.(*ReadResult)
			if !strings.Contains(result.Output, "file too large") {
				t.Fatalf("output = %q", result.Output)
			}
		}
	})
}

func TestReadOutputCapPaginationAndLineTruncation(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	lines := make([]string, 70)
	for i := range lines {
		lines[i] = fmt.Sprintf("%02d:%s", i, strings.Repeat("x", 995))
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cap.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, numbered := range []bool{false, true} {
		offset := 1
		var seen []string
		for {
			got, err := def.Handler(context.Background(), map[string]any{"path": "cap.txt", "offset": offset, "limit": 1000, "line_numbers": numbered})
			if err != nil {
				t.Fatal(err)
			}
			r := got.(ReadResult)
			if len([]rune(r.Output)) > readMaxOutputRunes {
				t.Fatalf("output exceeds cap: %d", len([]rune(r.Output)))
			}
			emitted := strings.Split(strings.TrimSuffix(r.Output, "\n"), "\n")
			if r.Output == "" {
				emitted = nil
			}
			if len(emitted) != r.EndLine-r.StartLine+1 {
				t.Fatalf("emitted %d lines; bounds %d-%d", len(emitted), r.StartLine, r.EndLine)
			}
			for i, line := range emitted {
				line = strings.TrimSuffix(line, "\r")
				if numbered {
					parts := strings.SplitN(line, "│", 2)
					if len(parts) != 2 {
						t.Fatalf("numbered line = %q", line)
					}
					var actual int
					if _, err := fmt.Sscanf(parts[0], "%d", &actual); err != nil {
						t.Fatal(err)
					}
					if actual != offset+i {
						t.Fatalf("line number = %d, want %d", actual, offset+i)
					}
					line = parts[1]
				}
				seen = append(seen, line)
			}
			if r.EndLine < offset {
				t.Fatalf("page at %d emitted no lines", offset)
			}
			if r.NextOffset == 0 {
				break
			}
			if r.NextOffset != r.EndLine+1 {
				t.Fatalf("next=%d end=%d", r.NextOffset, r.EndLine)
			}
			if r.NextOffset <= offset {
				t.Fatalf("pagination did not advance: %d", offset)
			}
			offset = r.NextOffset
		}
		if len(seen) != len(lines) {
			t.Fatalf("saw %d lines, want %d", len(seen), len(lines))
		}
		for i := range lines {
			if seen[i] != lines[i] {
				t.Fatalf("line %d differs across pages", i+1)
			}
		}
	}

	long := strings.Repeat("z", readMaxLineRunes+100) + "\nsecond\n"
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := def.Handler(context.Background(), map[string]any{"path": "long.txt", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	r := got.(ReadResult)
	if !strings.HasSuffix(r.Output, LineTruncationMarker+"\n") {
		t.Fatalf("truncated output lost line boundary: %q", r.Output[len(r.Output)-30:])
	}
	if r.EndLine != 1 || r.NextOffset != 2 {
		t.Fatalf("bounds end=%d next=%d", r.EndLine, r.NextOffset)
	}
	got, err = def.Handler(context.Background(), map[string]any{"path": "long.txt", "offset": 2, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.(ReadResult).Output != "second\n" {
		t.Fatalf("second page = %q", got.(ReadResult).Output)
	}
}

func TestReadTextPreservesSourceAndOptInNumbers(t *testing.T) {
	dir := t.TempDir()
	body := "\talpha\r\n beta\nlast"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	call := func(args map[string]any) *ReadResult {
		args["path"] = "sample.txt"
		got, err := def.Handler(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		returnValue, ok := got.(ReadResult)
		if !ok {
			t.Fatalf("result type %T, want ReadResult", got)
		}
		return &returnValue
	}
	if got := call(map[string]any{}).Output; got != body {
		t.Fatalf("default output = %q, want %q", got, body)
	}
	if got := call(map[string]any{"line_numbers": true}).Output; got != "     1│\talpha\r\n     2│ beta\n     3│last" {
		t.Fatalf("numbered output = %q", got)
	}
	if got := call(map[string]any{"offset": 2, "limit": 1}).Output; got != " beta\n" {
		t.Fatalf("range = %q", got)
	}
}

// TestReadResult_JSONShape verifies that ReadResult JSON output contains expected fields
// and does NOT contain the removed fields (resolved_path, truncation_reasons).
func TestReadResult_JSONShape(t *testing.T) {
	tmpDir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewReadTool(env)
	ctx := context.Background()

	t.Run("success paged read JSON has correct fields", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 1,
			"limit":  2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := json.Marshal(resultI)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}

		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}

		// Verify required fields exist
		requiredFields := []string{"path", "start_line", "end_line", "total_lines", "file_hash", "output", "next_offset"}
		for _, field := range requiredFields {
			if _, ok := m[field]; !ok {
				t.Errorf("missing required field: %s", field)
			}
		}

		// Verify removed fields are NOT present
		forbiddenFields := []string{"resolved_path", "truncation_reasons"}
		for _, field := range forbiddenFields {
			if _, ok := m[field]; ok {
				t.Errorf("forbidden field present in JSON: %s", field)
			}
		}
	})

	t.Run("success non-paged read JSON has no next_offset", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   "test.txt",
			"offset": 1,
			"limit":  100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := json.Marshal(resultI)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}

		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}

		// Verify next_offset is omitted entirely (the key must be absent).
		if v, ok := m["next_offset"]; ok {
			t.Errorf("next_offset key should be absent for non-paged read, got: %v", v)
		}

		// Verify file_hash is still present
		if _, ok := m["file_hash"]; !ok {
			t.Error("file_hash should be present")
		}

		// Verify removed fields are NOT present
		forbiddenFields := []string{"resolved_path", "truncation_reasons"}
		for _, field := range forbiddenFields {
			if _, ok := m[field]; ok {
				t.Errorf("forbidden field present in JSON: %s", field)
			}
		}
	})

	t.Run("error read JSON shape", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "nonexistent.txt",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := json.Marshal(resultI)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}

		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}

		// Error results should have path and output
		if _, ok := m["path"]; !ok {
			t.Error("missing path in error result")
		}
		if _, ok := m["output"]; !ok {
			t.Error("missing output in error result")
		}

		// Verify removed fields are NOT present
		forbiddenFields := []string{"resolved_path", "truncation_reasons"}
		for _, field := range forbiddenFields {
			if _, ok := m[field]; ok {
				t.Errorf("forbidden field present in error JSON: %s", field)
			}
		}

		// Error result should not have file_hash
		if _, ok := m["file_hash"]; ok {
			if v, ok := m["file_hash"].(string); ok && v != "" {
				t.Error("file_hash should be empty for error result")
			}
		}
	})
}

// TestReadTool_RejectsSpecialFile guards against the terminal-hijack class of
// bug: reading a non-regular file (here a FIFO, standing in for /dev/stdin)
// must fail fast via path policy rather than block on a read of the device.
func TestReadTool_RejectsSpecialFile(t *testing.T) {
	tmpDir := t.TempDir()
	fifoPath := filepath.Join(tmpDir, "fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skip("Mkfifo unsupported on this platform")
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewReadTool(env)

	type outcome struct{ err error }
	done := make(chan outcome, 1)
	go func() {
		_, err := toolDef.Handler(context.Background(), map[string]any{"path": "fifo"})
		done <- outcome{err: err}
	}()

	select {
	case res := <-done:
		if res.err == nil {
			t.Fatal("read of FIFO returned nil error, want rejection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read of FIFO blocked instead of being rejected by policy")
	}
}

func TestReadTool_StreamedHashAndTotalLines(t *testing.T) {
	big := strings.Repeat("line with trailing  \t\r\n"+strings.Repeat("x", 90)+"\n", 2000)
	tests := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"no trailing newline", "a\nb  \nc"},
		{"trailing newline", "a\nb\n"},
		{"crlf", "a\r\nb \r\nc\r\n"},
		{"only newline", "\n"},
		{"whitespace only tail", "a\n \t\r"},
		{"interior whitespace", "a \t b\n"},
		{"large multi-chunk", big},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "f.txt")
			if err := os.WriteFile(p, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			gotHash, gotLines, err := hashAndCountLines(p)
			if err != nil {
				t.Fatal(err)
			}
			wantLines := 0
			if len(tt.body) > 0 {
				wantLines = strings.Count(tt.body, "\n")
				if !strings.HasSuffix(tt.body, "\n") {
					wantLines++
				}
			}
			if gotHash != FileContentHash([]byte(tt.body)) {
				t.Errorf("hash = %s, want %s", gotHash, FileContentHash([]byte(tt.body)))
			}
			if gotLines != wantLines {
				t.Errorf("lines = %d, want %d", gotLines, wantLines)
			}
		})
	}

	t.Run("large file with offset and limit", func(t *testing.T) {
		if len(big) < 200*1024 {
			t.Fatalf("fixture too small: %d", len(big))
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o600); err != nil {
			t.Fatal(err)
		}
		def := NewReadTool(Env{WorkDir: dir, PathPolicy: func() *tool.PathPolicy { p := tool.NewPathPolicy(dir, config.PathsConfig{}); return &p }()})
		res, err := def.Handler(context.Background(), map[string]any{"path": "big.txt", "offset": 100, "limit": 5})
		if err != nil {
			t.Fatal(err)
		}
		r := res.(*ReadResult)
		if !strings.Contains(r.Output, "file too large") {
			t.Errorf("Output = %q, want size-limit error", r.Output)
		}
	})
}

func TestReadImageFile_OversizeStatCheck(t *testing.T) {
	p := filepath.Join(t.TempDir(), "huge.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(5*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = readImageFile(p, "huge.png")
	if err == nil || !strings.Contains(err.Error(), "file too large (max 5MB") {
		t.Fatalf("err = %v, want oversize error", err)
	}
}
