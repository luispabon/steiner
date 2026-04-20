package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type bashRequest struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

func runBash(ctx context.Context, payload []byte) (any, error) {
	req, err := decodeRequest[bashRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Command) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "command is required",
		}
	}

	cwd, err := resolveWorkingDir(req.Cwd)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", req.Command)
	cmd.Dir = cwd

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr != nil {
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ProcessState != nil {
			exitCode = exitErr.ProcessState.ExitCode()
		}
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "command_failed",
			Message: runErr.Error(),
			Details: map[string]any{
				"command":   req.Command,
				"cwd":       cwd,
				"stdout":    stdout.String(),
				"stderr":    stderr.String(),
				"exit_code": exitCode,
			},
		}
	}

	return map[string]any{
		"command": req.Command,
		"cwd":     cwd,
		"stdout":  stdout.String(),
		"stderr":  stderr.String(),
		"exit_code": 0,
	}, nil
}

func resolveWorkingDir(cwd string) (string, error) {
	base, err := os.Getwd()
	if err != nil {
		return "", &steinertool.JSONEnvelopeError{
			Kind:    "cwd_error",
			Message: err.Error(),
		}
	}
	if strings.TrimSpace(cwd) == "" {
		return base, nil
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd), nil
	}
	return filepath.Clean(filepath.Join(base, cwd)), nil
}
