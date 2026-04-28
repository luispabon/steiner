package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("fake stdin error")
}

func decodeEnvelope(t *testing.T, data []byte) steinertool.JSONEnvelope {
	t.Helper()
	var env steinertool.JSONEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, string(data))
	}
	return env
}

func TestRun_MissingSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if g, w := stderr.String(), "missing core tool subcommand\n"; g != w {
		t.Errorf("stderr = %q, want %q", g, w)
	}

	env := decodeEnvelope(t, stdout.Bytes())
	if env.OK {
		t.Error("OK = true, want false")
	}
	if env.Error == nil {
		t.Fatal("Error = nil, want non-nil")
	}
	if g, w := env.Error.Kind, "usage_error"; g != w {
		t.Errorf("Error.Kind = %q, want %q", g, w)
	}
	if g, w := env.Error.Message, "missing core tool subcommand"; g != w {
		t.Errorf("Error.Message = %q, want %q", g, w)
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"nonexistent"}, strings.NewReader(""), &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	wantStderr := `unknown core tool "nonexistent"` + "\n"
	if g := stderr.String(); g != wantStderr {
		t.Errorf("stderr = %q, want %q", g, wantStderr)
	}

	env := decodeEnvelope(t, stdout.Bytes())
	if env.OK {
		t.Error("OK = true, want false")
	}
	if env.Error == nil {
		t.Fatal("Error = nil, want non-nil")
	}
	if g, w := env.Error.Kind, "usage_error"; g != w {
		t.Errorf("Error.Kind = %q, want %q", g, w)
	}
	wantMsg := `unknown core tool "nonexistent"`
	if g := env.Error.Message; g != wantMsg {
		t.Errorf("Error.Message = %q, want %q", g, wantMsg)
	}
}

func TestRun_StdinReadFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"read"}, failingReader{}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	env := decodeEnvelope(t, stdout.Bytes())
	if env.OK {
		t.Error("OK = true, want false")
	}
	if env.Error == nil {
		t.Fatal("Error = nil, want non-nil")
	}
	if g, w := env.Error.Kind, "stdin_error"; g != w {
		t.Errorf("Error.Kind = %q, want %q", g, w)
	}
	if g, w := env.Error.Message, "fake stdin error"; g != w {
		t.Errorf("Error.Message = %q, want %q", g, w)
	}
}

func TestRun_HandlerSuccess(t *testing.T) {
	handlers["test_handler"] = func(_ context.Context, _ []byte) (any, error) {
		return map[string]any{"result": "ok"}, nil
	}
	t.Cleanup(func() { delete(handlers, "test_handler") })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"test_handler"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	env := decodeEnvelope(t, stdout.Bytes())
	if !env.OK {
		t.Error("OK = false, want true")
	}
	if env.Error != nil {
		t.Errorf("Error = %v, want nil", env.Error)
	}
	result, ok := env.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result type = %T, want map[string]any", env.Result)
	}
	if g, w := result["result"], "ok"; g != w {
		t.Errorf("Result[result] = %v, want %v", g, w)
	}
}

func TestRun_HandlerError(t *testing.T) {
	testErr := &steinertool.JSONEnvelopeError{
		Kind:    "handler_error",
		Message: "something broke",
	}
	handlers["test_handler"] = func(_ context.Context, _ []byte) (any, error) {
		return nil, testErr
	}
	t.Cleanup(func() { delete(handlers, "test_handler") })

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"test_handler"}, strings.NewReader(""), &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	env := decodeEnvelope(t, stdout.Bytes())
	if env.OK {
		t.Error("OK = true, want false")
	}
	if env.Error == nil {
		t.Fatal("Error = nil, want non-nil")
	}
	if g, w := env.Error.Kind, "handler_error"; g != w {
		t.Errorf("Error.Kind = %q, want %q", g, w)
	}
	if g, w := env.Error.Message, "something broke"; g != w {
		t.Errorf("Error.Message = %q, want %q", g, w)
	}
}

func TestDecodeRequest(t *testing.T) {
	type testStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	tests := []struct {
		name      string
		payload   []byte
		wantValue testStruct
		wantErr   bool
		wantKind  string
	}{
		{
			name:    "empty payload returns zero value",
			payload: []byte{},
		},
		{
			name:    "whitespace-only payload returns zero value",
			payload: []byte("  \n\t  "),
		},
		{
			name:     "invalid JSON returns invalid_input error",
			payload:  []byte("{invalid}"),
			wantErr:  true,
			wantKind: "invalid_input",
		},
		{
			name:      "valid JSON decodes successfully",
			payload:   []byte(`{"name":"hello","age":30}`),
			wantValue: testStruct{Name: "hello", Age: 30},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeRequest[testStruct](tt.payload)
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want non-nil")
				}
				var envelopeErr *steinertool.JSONEnvelopeError
				if !errors.As(err, &envelopeErr) {
					t.Fatalf("err type = %T, want *steinertool.JSONEnvelopeError", err)
				}
				if envelopeErr.Kind != tt.wantKind {
					t.Errorf("Kind = %q, want %q", envelopeErr.Kind, tt.wantKind)
				}
				return
			}
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if got != tt.wantValue {
				t.Errorf("got = %+v, want %+v", got, tt.wantValue)
			}
		})
	}
}

func TestToEnvelopeError(t *testing.T) {
	sentinel := errors.New("something failed")
	customErr := &steinertool.JSONEnvelopeError{
		Kind:    "custom",
		Message: "custom error",
	}

	tests := []struct {
		name     string
		err      error
		wantNil  bool
		wantKind string
		wantMsg  string
	}{
		{
			name:    "nil returns nil",
			err:     nil,
			wantNil: true,
		},
		{
			name:     "plain error wraps as internal",
			err:      sentinel,
			wantKind: "internal",
			wantMsg:  "something failed",
		},
		{
			name:     "JSONEnvelopeError passes through unchanged",
			err:      customErr,
			wantKind: "custom",
			wantMsg:  "custom error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toEnvelopeError(tt.err)
			if tt.wantNil {
				if got != nil {
					t.Errorf("toEnvelopeError() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("toEnvelopeError() = nil, want non-nil")
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", got.Message, tt.wantMsg)
			}
		})
	}
}
