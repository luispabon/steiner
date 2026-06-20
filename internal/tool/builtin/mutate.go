package builtin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// NewMutateTool creates a ToolDef for the mutate tool.
func NewMutateTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "mutate",
		Description:     "Create, overwrite, replace, line-replace, delete_line, insert-before, insert-after, delete, or move files. Supports file_hash for staleness detection on existing targets — pass the hash from read/grep to fail fast if the file changed. Move rejects destination collisions instead of overwriting. Use mutate for all file edits; do not use bash, sed, cat, write, edit, or apply_patch for file mutations.",
		ParameterSchema: MutateSchema(),
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[MutateInput](input)
			if err != nil {
				return nil, fmt.Errorf("mutate: %w", err)
			}
			planner := &mutatePlanner{
				env:    env,
				states: make(map[string]*mutateFileState),
				result: MutateResult{DryRun: in.DryRun},
			}
			result := planner.run(in)
			return result, nil
		},
	}
}

var allowedFields = map[string]map[string]struct{}{
	"create": {
		"path":           {},
		"content":        {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
		"allow_empty":    {},
	},
	"write": {
		"path":           {},
		"content":        {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
		"allow_empty":    {},
	},
	"replace": {
		"path":           {},
		"old_string":     {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"replace_all":    {},
		"file_hash":      {},
	},
	"line_replace": {
		"path":           {},
		"line":           {},
		"line_count":     {},
		"old_string":     {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"delete_line": {
		"path":           {},
		"line":           {},
		"line_count":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"delete": {
		"path":      {},
		"file_hash": {},
	},
	"move": {
		"from":           {},
		"to":             {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"insert_before": {
		"path":           {},
		"line":           {},
		"content":        {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"insert_after": {
		"path":           {},
		"line":           {},
		"content":        {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
}

var validFieldsByOpType = func() map[string][]string {
	types := make(map[string][]string, len(allowedFields))
	for opType, fields := range allowedFields {
		valid := make([]string, 0, len(fields))
		for field := range fields {
			valid = append(valid, field)
		}
		sort.Strings(valid)
		types[opType] = valid
	}
	return types
}()

func validateFields(index int, op MutateOperation) error {
	opType := strings.TrimSpace(op.Type)
	allowed, ok := allowedFields[opType]
	if !ok {
		return nil
	}
	type fieldCheck struct {
		name  string
		isSet bool
	}
	checks := []fieldCheck{
		{"path", op.Path != ""},
		{"content", op.Content != ""},
		{"old_string", op.OldString != ""},
		{"new_string", op.NewString != ""},
		{"assert_present", len(op.AssertPresent) > 0},
		{"assert_absent", len(op.AssertAbsent) > 0},
		{"replace_all", op.ReplaceAll},
		{"line", op.Line != 0},
		{"line_count", op.LineCount != 0},
		{"file_hash", op.FileHash != ""},
		{"allow_empty", op.AllowEmpty},
		{"from", op.From != ""},
		{"to", op.To != ""},
	}
	for _, c := range checks {
		if c.isSet {
			if _, ok := allowed[c.name]; !ok {
				return fmt.Errorf("mutate: operation %d %s: field %q is not valid for this operation type; valid fields: %s", index, opType, c.name, strings.Join(validFieldsByOpType[opType], ", "))
			}
		}
	}
	return nil
}
