package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type writeRequest struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

func runWrite(ctx context.Context, payload []byte) (any, error) {
	_ = ctx

	req, err := decodeRequest[writeRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "path is required",
		}
	}

	if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "write_error",
			Message: err.Error(),
			Details: map[string]any{"path": req.Path},
		}
	}

	if err := os.WriteFile(req.Path, []byte(req.Contents), 0o644); err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "write_error",
			Message: err.Error(),
			Details: map[string]any{"path": req.Path},
		}
	}

	return map[string]any{
		"path":         req.Path,
		"bytes_written": len(req.Contents),
	}, nil
}
