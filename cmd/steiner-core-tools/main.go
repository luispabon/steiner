package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type handler func(context.Context, []byte) (any, error)

var handlers = map[string]handler{
	"read":   runRead,
	"write":  runWrite,
	"edit":   runEdit,
	"glob":   runGlob,
	"search": runSearch,
	"bash":   runBash,
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_ = writeEnvelope(stdout, steinertool.JSONEnvelope{
			OK: false,
			Error: &steinertool.JSONEnvelopeError{
				Kind:    "usage_error",
				Message: "missing core tool subcommand",
			},
		})
		_, _ = fmt.Fprintln(stderr, "missing core tool subcommand")
		return 1
	}

	handler, ok := handlers[args[0]]
	if !ok {
		_ = writeEnvelope(stdout, steinertool.JSONEnvelope{
			OK: false,
			Error: &steinertool.JSONEnvelopeError{
				Kind:    "usage_error",
				Message: fmt.Sprintf("unknown core tool %q", args[0]),
			},
		})
		_, _ = fmt.Fprintf(stderr, "unknown core tool %q\n", args[0])
		return 1
	}

	payload, err := io.ReadAll(stdin)
	if err != nil {
		_ = writeEnvelope(stdout, steinertool.JSONEnvelope{
			OK: false,
			Error: &steinertool.JSONEnvelopeError{
				Kind:    "stdin_error",
				Message: err.Error(),
			},
		})
		return 1
	}

	result, err := handler(ctx, payload)
	if err != nil {
		_ = writeEnvelope(stdout, steinertool.JSONEnvelope{
			OK:    false,
			Error: toEnvelopeError(err),
		})
		return 1
	}

	if err := writeEnvelope(stdout, steinertool.JSONEnvelope{OK: true, Result: result}); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	return 0
}

func decodeRequest[T any](payload []byte) (T, error) {
	var req T
	if len(strings.TrimSpace(string(payload))) == 0 {
		return req, nil
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: err.Error(),
		}
	}
	return req, nil
}

func writeEnvelope(w io.Writer, env steinertool.JSONEnvelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

func toEnvelopeError(err error) *steinertool.JSONEnvelopeError {
	if err == nil {
		return nil
	}
	var envelopeErr *steinertool.JSONEnvelopeError
	if errors.As(err, &envelopeErr) {
		return envelopeErr
	}
	return &steinertool.JSONEnvelopeError{
		Kind:    "internal",
		Message: err.Error(),
	}
}
