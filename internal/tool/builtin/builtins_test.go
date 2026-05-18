package builtin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBuiltins(t *testing.T) {
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
	env := Env{WorkDir: t.TempDir(), PathPolicy: &policy}
	tools := Builtins(env)

	t.Run("all 9 builtin tools registered", func(t *testing.T) {
		if len(tools) != 9 {
			t.Fatalf("Builtins returned %d tools, want 9", len(tools))
		}
	})

	t.Run("correct tool names", func(t *testing.T) {
		names := make(map[string]bool)
		for _, td := range tools {
			names[td.Name] = true
		}
		want := []string{"read", "glob", "grep", "ls", "bash", "display_file", "scratchpad", "mutate", "fetch_url"}
		for _, w := range want {
			if !names[w] {
				t.Errorf("missing tool %q", w)
			}
		}
	})

	t.Run("old tool names absent", func(t *testing.T) {
		names := make(map[string]bool)
		for _, td := range tools {
			names[td.Name] = true
		}
		if names["search"] {
			t.Error("old tool name 'search' is still registered")
		}
		if names["list"] {
			t.Error("old tool name 'list' is still registered")
		}
	})

	t.Run("write, edit, and apply_patch are not builtin tools", func(t *testing.T) {
		names := make(map[string]bool)
		for _, td := range tools {
			names[td.Name] = true
		}
		if names["write"] {
			t.Error("builtin tool 'write' should not be registered")
		}
		if names["edit"] {
			t.Error("builtin tool 'edit' should not be registered")
		}
		if names["apply_patch"] {
			t.Error("builtin tool 'apply_patch' should not be registered")
		}
	})

	t.Run("builtin tools do not hardcode approval defaults", func(t *testing.T) {
		alwaysAuto := map[string]bool{"scratchpad": true}
		for _, td := range tools {
			if alwaysAuto[td.Name] {
				continue
			}
			if td.Approval != "" {
				t.Errorf("tool %q Approval = %q, want empty", td.Name, td.Approval)
			}
		}
	})

	t.Run("tool result is provider-serializable", func(t *testing.T) {
		results := []any{
			&ReadResult{Path: "test.txt", StartLine: 1, EndLine: 5, TotalLines: 10, Output: "hello\nworld\n"},
			&MutationResult{Path: "test.txt", Output: "file written"},
			&Result{Output: "file1\nfile2\n", Returned: 2, NextOffset: 5},
			&GrepResult{Matches: 3, Returned: 3, Output: "match1\nmatch2\n"},
			&BashResult{ExitCode: 0, Output: "hello", Truncated: false},
			&DisplayFileResult{Path: "test.txt", Status: "displayed"},
			&ApplyPatchResult{
				Paths:        []string{"test.txt"},
				Moved:        []MoveResult{{From: "old.txt", To: "new.txt"}},
				HunksApplied: 3,
				Output:       "Success.\nUpdated the following files:\nM test.txt",
			},
			&MutateResult{
				Paths:             []string{"test.txt"},
				Modified:          []string{"test.txt"},
				OperationsApplied: 1,
				Output:            "Success.\nUpdated the following files:\nM test.txt",
			},
			&FetchURLResult{
				URL:           "http://example.com",
				Title:         "Example",
				Description:   "Example site",
				Content:       "Hello world",
				ContentLength: 11,
			},
		}
		for i, r := range results {
			data, err := json.Marshal(r)
			if err != nil {
				t.Errorf("result %d (%T) serialization error: %v", i, r, err)
				continue
			}
			if len(data) == 0 {
				t.Errorf("result %d (%T) produced empty JSON", i, r)
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Errorf("result %d (%T) JSON unmarshal error: %v", i, r, err)
			}
		}
	})

	t.Run("tool error is model-visible", func(t *testing.T) {
		for _, td := range tools {
			errResult, err := td.Handler(context.Background(), map[string]any{})
			if err != nil {
				continue
			}

			_, isResult := errResult.(ReadResult)
			_, isPtrResult := errResult.(*ReadResult)
			_, isGlobResult := errResult.(Result)
			_, isPtrGlob := errResult.(*Result)
			_, isGrepResult := errResult.(GrepResult)
			_, isMutation := errResult.(*MutationResult)
			_, isBash := errResult.(*BashResult)
			_, isDisplayFile := errResult.(*DisplayFileResult)
			_, isApplyPatch := errResult.(*ApplyPatchResult)
			_, isMutate := errResult.(*MutateResult)

			hasResult := isResult || isPtrResult || isGlobResult || isPtrGlob || isGrepResult || isMutation || isBash || isDisplayFile || isApplyPatch || isMutate
			if !hasResult {
				t.Errorf("tool %q: empty input returned nil error and unrecognized result type %T, expected error", td.Name, errResult)
			}
		}
	})

	t.Run("handler returns errors for invalid input", func(t *testing.T) {
		for _, td := range tools {
			td := td
			t.Run(td.Name, func(_ *testing.T) {
				_, err := td.Handler(context.Background(), map[string]any{
					"nonexistent_key": "value",
				})
				_ = err
			})
		}
	})
}
