package builtin

import (
	"context"
	"strings"
	"testing"
)

func TestApplyPatchInputAcceptsPatchAndDryRun(t *testing.T) {
	in, err := decodeInput[ApplyPatchInput](map[string]any{
		"patch":   "*** Begin Patch\n*** End Patch",
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("decodeInput(apply_patch) error = %v", err)
	}
	if got, want := in.Patch, "*** Begin Patch\n*** End Patch"; got != want {
		t.Fatalf("Patch = %q, want %q", got, want)
	}
	if !in.DryRun {
		t.Fatal("DryRun = false, want true")
	}
}

func TestApplyPatchInputRejectsLegacyFields(t *testing.T) {
	_, err := decodeInput[ApplyPatchInput](map[string]any{
		"path": "note.txt",
		"hunks": []any{
			map[string]any{"old": "hello", "new": "world"},
		},
	})
	if err == nil {
		t.Fatal("decodeInput(apply_patch legacy input) = nil error, want unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decodeInput(apply_patch legacy input) error = %v, want unknown field error", err)
	}
}

func TestApplyPatchToolParserPlaceholder(t *testing.T) {
	result, err := NewApplyPatchTool(Env{}).Handler(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** End Patch",
	})
	if err == nil {
		t.Fatal("Handler() error = nil, want parser not implemented")
	}
	if !strings.Contains(err.Error(), "apply_patch: parser not implemented") {
		t.Fatalf("Handler() error = %v, want parser not implemented", err)
	}
	if result != nil {
		t.Fatalf("Handler() result = %#v, want nil", result)
	}
}

