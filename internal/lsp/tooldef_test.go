package lsp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestToolDefsCount(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	defs := ToolDefs(m)
	if len(defs) != 3 {
		t.Fatalf("ToolDefs returned %d tools, want 3", len(defs))
	}
}

func TestToolDefsNames(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	defs := ToolDefs(m)
	expectedNames := []string{"definitions", "references", "diagnostics"}

	for i, expected := range expectedNames {
		if i >= len(defs) {
			t.Fatalf("ToolDefs returned %d tools, expected at least %d", len(defs), i+1)
		}
		if defs[i].Name != expected {
			t.Errorf("ToolDefs[%d].Name = %q, want %q", i, defs[i].Name, expected)
		}
	}
}

func TestToolDefsOrdering(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.LSPConfig
	}{
		{
			name: "empty servers",
			cfg:  config.LSPConfig{},
		},
		{
			name: "servers in reverse order",
			cfg: config.LSPConfig{
				Servers: map[string]config.LSPServerConfig{
					"z-server": {Enabled: true},
					"a-server": {Enabled: true},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m1 := NewManager(tt.cfg, "/workspace", nil, func(string) {}, nil)
			defer m1.Close()
			defs1 := ToolDefs(m1)

			m2 := NewManager(tt.cfg, "/workspace", nil, func(string) {}, nil)
			defer m2.Close()
			defs2 := ToolDefs(m2)

			if len(defs1) != len(defs2) {
				t.Errorf("ToolDefs count mismatch: %d vs %d", len(defs1), len(defs2))
				return
			}

			for i := range defs1 {
				if defs1[i].Name != defs2[i].Name {
					t.Errorf("ToolDefs[%d].Name mismatch: %q vs %q", i, defs1[i].Name, defs2[i].Name)
				}
			}

			expectedOrder := []string{"definitions", "references", "diagnostics"}
			for i, expected := range expectedOrder {
				if i >= len(defs1) {
					break
				}
				if defs1[i].Name != expected {
					t.Errorf("Tool at position %d: got %q, want %q", i, defs1[i].Name, expected)
				}
			}
		})
	}
}

func TestToolDefsParallelSafe(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	defs := ToolDefs(m)
	for _, def := range defs {
		if !def.ParallelSafe {
			t.Errorf("Tool %q: ParallelSafe = false, want true", def.Name)
		}
	}
}

func TestToolDefsSchemas(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	defs := ToolDefs(m)

	tests := []struct {
		name     string
		toolIdx  int
		required []string
	}{
		{
			name:     "definitions schema",
			toolIdx:  0,
			required: []string{"file", "line", "column"},
		},
		{
			name:     "references schema",
			toolIdx:  1,
			required: []string{"file", "line", "column"},
		},
		{
			name:     "diagnostics schema",
			toolIdx:  2,
			required: []string{"file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := defs[tt.toolIdx].ParameterSchema
			props, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema has no properties object")
			}

			for _, req := range tt.required {
				if _, ok := props[req]; !ok {
					t.Errorf("required field %q not in properties", req)
				}
			}

			reqList, ok := schema["required"].([]string)
			if !ok {
				t.Fatalf("schema has no required array")
			}
			for _, req := range tt.required {
				found := false
				for _, r := range reqList {
					if r == req {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("required field %q not in required list", req)
				}
			}
		})
	}
}

func TestReferencesIncludeDeclarationDefault(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"test": {
				Enabled:        true,
				FileExtensions: []string{".test"},
				Command:        "echo",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	defs := ToolDefs(m)
	refTool := defs[1]

	schema := refTool.ParameterSchema
	props := schema["properties"].(map[string]any)
	decl := props["include_declaration"].(map[string]any)

	defaultVal, ok := decl["default"]
	if !ok {
		t.Errorf("include_declaration schema missing default")
	}
	if v, ok := defaultVal.(bool); ok && !v {
		t.Errorf("include_declaration default = %v, want true", v)
	}
}

func TestNoServerError(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	defs := ToolDefs(m)

	input := map[string]any{
		"file":   "test.unknown",
		"line":   1.0,
		"column": 1.0,
	}

	result, err := defs[0].Handler(context.Background(), input)
	if err != nil {
		t.Errorf("definitions handler returned error: %v", err)
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	if !strings.Contains(msg, "No language server is configured") {
		t.Errorf("message does not contain expected unavailability text: %q", msg)
	}
	if !strings.Contains(msg, ".unknown") {
		t.Errorf("message does not contain file extension: %q", msg)
	}
}

func TestServerExitedError(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"mock": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "true",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	result, goErr := handleNavigationError(m, "test.go", errServerExited)
	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if goErr != nil {
		t.Fatalf("expected nil error, got %v", goErr)
	}

	if !strings.Contains(msg, "exited unexpectedly") {
		t.Errorf("message does not contain expected text: %q", msg)
	}
}

func TestFailedServerError(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"mock": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "true",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer m.Close()

	err := errors.New("other error")

	result, goErr := handleNavigationError(m, "test.go", err)
	if result != nil {
		t.Errorf("handleNavigationError returned non-nil result for non-unavailable error: %v", result)
	}
	if goErr == nil || goErr != err {
		t.Errorf("handleNavigationError should return original error, got %v", goErr)
	}
}
