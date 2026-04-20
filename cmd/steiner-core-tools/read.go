package main

import (
	"context"
	"os"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type readRequest struct {
	Path string `json:"path"`
}

func runRead(ctx context.Context, payload []byte) (any, error) {
	_ = ctx

	req, err := decodeRequest[readRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "path is required",
		}
	}

	contents, err := os.ReadFile(req.Path)
	if err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "read_error",
			Message: err.Error(),
			Details: map[string]any{"path": req.Path},
		}
	}

	return map[string]any{
		"path":     req.Path,
		"contents": string(contents),
	}, nil
}
