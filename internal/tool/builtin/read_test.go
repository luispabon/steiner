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
		expected := fileContentHash([]byte(content))
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

	t.Run("total output capped is recoverable via NextOffset", func(t *testing.T) {
		const lineCount = 80
		const contentLength = 1896
		lines := make([]string, lineCount)
		for i := range lines {
			lines[i] = fmt.Sprintf("%02d:%s", i, strings.Repeat("x", contentLength-3))
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "paged-large.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write paged file: %v", err)
		}
		offset := 1
		var collected []string
		for {
			resultI, err := toolDef.Handler(ctx, map[string]any{"path": "paged-large.txt", "offset": offset, "limit": 1000})
			if err != nil {
				t.Fatalf("read page at offset %d: %v", offset, err)
			}
			result := resultI.(ReadResult)
			if result.StartLine != offset {
				t.Fatalf("StartLine = %d, want %d", result.StartLine, offset)
			}
			for _, rendered := range strings.Split(result.Output, "\n") {
				parts := strings.SplitN(rendered, "\t", 2)
				if len(parts) != 2 {
					t.Fatalf("rendered line = %q, want line number and content", rendered)
				}
				collected = append(collected, parts[1])
			}
			if result.NextOffset == 0 {
				break
			}
			if result.NextOffset <= offset || result.EndLine != result.NextOffset-1 {
				t.Fatalf("page bounds: offset=%d end=%d next=%d", offset, result.EndLine, result.NextOffset)
			}
			offset = result.NextOffset
		}
		if len(collected) != len(lines) {
			t.Fatalf("collected %d lines, want %d", len(collected), len(lines))
		}
		for i := range lines {
			if collected[i] != lines[i] {
				t.Fatalf("collected line %d differs from source", i+1)
			}
		}
	})
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

		// Verify next_offset is omitted (not present or null)
		if v, ok := m["next_offset"]; ok {
			if v != nil && v != 0.0 {
				t.Errorf("next_offset should be omitted for non-paged read, got: %v", v)
			}
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
