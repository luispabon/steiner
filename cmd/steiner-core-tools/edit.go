package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type editRequest struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func runEdit(ctx context.Context, payload []byte) (any, error) {
	_ = ctx

	req, err := decodeRequest[editRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "path is required",
		}
	}
	if strings.TrimSpace(req.Old) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "old is required",
		}
	}

	path, err := resolveEditablePath(req.Path)
	if err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "edit_error",
			Message: err.Error(),
			Details: map[string]any{"path": req.Path},
		}
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "edit_error",
			Message: err.Error(),
			Details: map[string]any{"path": path},
		}
	}

	current := string(contents)
	occurrences := strings.Count(current, req.Old)
	switch {
	case occurrences == 0:
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "edit_error",
			Message: "old snippet not found",
			Details: map[string]any{
				"path":        path,
				"old":         req.Old,
				"occurrences": occurrences,
			},
		}
	case occurrences > 1:
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "edit_error",
			Message: "old snippet must match exactly once",
			Details: map[string]any{
				"path":        path,
				"old":         req.Old,
				"occurrences": occurrences,
			},
		}
	}

	updated := strings.Replace(current, req.Old, req.New, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "edit_error",
			Message: err.Error(),
			Details: map[string]any{"path": path},
		}
	}

	return map[string]any{
		"path":         path,
		"replacements": 1,
		"old_bytes":    len(req.Old),
		"new_bytes":    len(req.New),
	}, nil
}

func resolveEditablePath(raw string) (string, error) {
	base, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return "", os.ErrInvalid
	}

	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)

	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside current working directory %q", raw, base)
	}
	return path, nil
}
