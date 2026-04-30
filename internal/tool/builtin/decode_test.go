package builtin

import (
	"testing"
)

func TestDecodeInput(t *testing.T) {
	type signedByteInput struct {
		Value int8 `json:"value"`
	}
	type unsignedByteInput struct {
		Value uint8 `json:"value"`
	}

	t.Run("valid ReadInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{
			"path":   "test.txt",
			"offset": 5,
			"limit":  100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "test.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "test.txt")
		}
		if result.Offset != 5 {
			t.Errorf("Offset = %d, want %d", result.Offset, 5)
		}
		if result.Limit != 100 {
			t.Errorf("Limit = %d, want %d", result.Limit, 100)
		}
	})

	t.Run("valid WriteInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[WriteInput](map[string]any{
			"path":    "test.txt",
			"content": "hello world",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "test.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "test.txt")
		}
		if result.Content != "hello world" {
			t.Errorf("Content = %q, want %q", result.Content, "hello world")
		}
	})

	t.Run("valid EditInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[EditInput](map[string]any{
			"path":        "test.txt",
			"old_string":  "foo",
			"new_string":  "bar",
			"replace_all": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "test.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "test.txt")
		}
		if result.OldString != "foo" {
			t.Errorf("OldString = %q, want %q", result.OldString, "foo")
		}
		if result.NewString != "bar" {
			t.Errorf("NewString = %q, want %q", result.NewString, "bar")
		}
		if !result.ReplaceAll {
			t.Error("ReplaceAll should be true")
		}
	})

	t.Run("unknown fields are rejected", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path":          "test.txt",
			"unknown_field": "value",
		})
		if err == nil {
			t.Fatal("expected error for unknown field")
		}
	})

	t.Run("invalid output_mode strings accepted at decode level", func(t *testing.T) {
		result, err := decodeInput[GrepInput](map[string]any{
			"pattern":     "foo",
			"output_mode": "invalid_value",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.OutputMode != "invalid_value" {
			t.Errorf("OutputMode = %q, want %q", result.OutputMode, "invalid_value")
		}
	})

	t.Run("missing required fields are accepted at decode level", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "" {
			t.Errorf("Path = %q, want empty string", result.Path)
		}
	})

	t.Run("defaults are not applied by decode", func(t *testing.T) {
		result, err := decodeInput[GlobInput](map[string]any{
			"pattern": "*.go",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Limit != 0 {
			t.Errorf("Limit = %d, want 0 (default not applied)", result.Limit)
		}
		if result.Offset != 0 {
			t.Errorf("Offset = %d, want 0 (default not applied)", result.Offset)
		}
	})

	t.Run("valid BashInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[BashInput](map[string]any{
			"command":          "echo hello",
			"cwd":              "/tmp",
			"timeout_seconds":  60,
			"max_output_chars": 50000,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Command != "echo hello" {
			t.Errorf("Command = %q, want %q", result.Command, "echo hello")
		}
		if result.CWD != "/tmp" {
			t.Errorf("CWD = %q, want %q", result.CWD, "/tmp")
		}
	})

	t.Run("float64 coerced to string", func(t *testing.T) {
		result, err := decodeInput[EditInput](map[string]any{
			"path":       "test.txt",
			"old_string": "foo",
			"new_string": float64(170),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.NewString != "170" {
			t.Errorf("NewString = %q, want %q", result.NewString, "170")
		}
	})

	t.Run("float64 coerced to int", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{
			"path":   "test.txt",
			"offset": float64(5),
			"limit":  float64(100),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Offset != 5 {
			t.Errorf("Offset = %d, want %d", result.Offset, 5)
		}
		if result.Limit != 100 {
			t.Errorf("Limit = %d, want %d", result.Limit, 100)
		}
	})

	t.Run("integral float accepted for signed ints", func(t *testing.T) {
		result, err := decodeInput[signedByteInput](map[string]any{
			"value": float64(5),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Value != 5 {
			t.Errorf("Value = %d, want %d", result.Value, 5)
		}
	})

	t.Run("fractional float rejected for signed ints", func(t *testing.T) {
		_, err := decodeInput[signedByteInput](map[string]any{
			"value": float64(3.9),
		})
		if err == nil {
			t.Fatal("expected error for fractional float")
		}
	})

	t.Run("negative float rejected for unsigned ints", func(t *testing.T) {
		_, err := decodeInput[unsignedByteInput](map[string]any{
			"value": float64(-1),
		})
		if err == nil {
			t.Fatal("expected error for negative float")
		}
	})

	t.Run("out of range float rejected for signed ints", func(t *testing.T) {
		_, err := decodeInput[signedByteInput](map[string]any{
			"value": float64(128),
		})
		if err == nil {
			t.Fatal("expected error for out of range float")
		}
	})

	t.Run("out of range float rejected for unsigned ints", func(t *testing.T) {
		_, err := decodeInput[unsignedByteInput](map[string]any{
			"value": float64(256),
		})
		if err == nil {
			t.Fatal("expected error for out of range float")
		}
	})

	t.Run("string coerced to int", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{
			"path":   "test.txt",
			"offset": "5",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Offset != 5 {
			t.Errorf("Offset = %d, want %d", result.Offset, 5)
		}
	})

	t.Run("valid LSInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[LSInput](map[string]any{
			"path":      ".",
			"recursive": true,
			"limit":     50,
			"offset":    10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "." {
			t.Errorf("Path = %q, want %q", result.Path, ".")
		}
		if !result.Recursive {
			t.Error("Recursive should be true")
		}
		if result.Limit != 50 {
			t.Errorf("Limit = %d, want %d", result.Limit, 50)
		}
		if result.Offset != 10 {
			t.Errorf("Offset = %d, want %d", result.Offset, 10)
		}
	})
}
