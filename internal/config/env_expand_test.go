package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEnvExpanderBasicExpansion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		env     map[string]string
		want    string
		wantErr []string
	}{
		{
			name:  "double dollar escape",
			input: "$$VAR",
			env:   map[string]string{"VAR": "value"},
			want:  "$VAR",
		},
		{
			name:  "braced form with value",
			input: "${VAR}",
			env:   map[string]string{"VAR": "value"},
			want:  "value",
		},
		{
			name:    "braced form undefined no default",
			input:   "${VAR}",
			env:     map[string]string{},
			want:    "",
			wantErr: []string{"VAR"},
		},
		{
			name:  "braced form undefined with default",
			input: "${VAR:-default}",
			env:   map[string]string{},
			want:  "default",
		},
		{
			name:  "braced form empty with default",
			input: "${VAR:-default}",
			env:   map[string]string{"VAR": ""},
			want:  "default",
		},
		{
			name:  "braced form empty no default",
			input: "${VAR}",
			env:   map[string]string{"VAR": ""},
			want:  "",
		},
		{
			name:  "braced form empty with empty default",
			input: "${VAR:-}",
			env:   map[string]string{},
			want:  "",
		},
		{
			name:  "bare form with value",
			input: "$VAR",
			env:   map[string]string{"VAR": "value"},
			want:  "value",
		},
		{
			name:    "bare form undefined",
			input:   "$VAR",
			env:     map[string]string{},
			want:    "",
			wantErr: []string{"VAR"},
		},
		{
			name:  "invalid env name in braces",
			input: "${not-a-name}",
			env:   map[string]string{},
			want:  "${not-a-name}",
		},
		{
			name:  "unterminated brace",
			input: "${VAR",
			env:   map[string]string{"VAR": "value"},
			want:  "${VAR",
		},
		{
			name:  "digit is not token",
			input: "$5",
			env:   map[string]string{},
			want:  "$5",
		},
		{
			name:  "space after dollar is not token",
			input: "$ space",
			env:   map[string]string{},
			want:  "$ space",
		},
		{
			name:  "trailing dollar",
			input: "text$",
			env:   map[string]string{},
			want:  "text$",
		},
		{
			name:  "default with literal text",
			input: "${VAR:-default-value}",
			env:   map[string]string{},
			want:  "default-value",
		},
		{
			name:  "default with another variable",
			input: "${VAR:-$FALLBACK}",
			env:   map[string]string{"FALLBACK": "fallback"},
			want:  "fallback",
		},
		{
			name:  "multiple variables in one string",
			input: "${VAR1}${VAR2}",
			env:   map[string]string{"VAR1": "a", "VAR2": "b"},
			want:  "ab",
		},
		{
			name:    "multiple undefined variables",
			input:   "${VAR1}${VAR2}",
			env:     map[string]string{},
			want:    "",
			wantErr: []string{"VAR1", "VAR2"},
		},
		{
			name:  "numeric-looking expansion stays string",
			input: "${PORT}",
			env:   map[string]string{"PORT": "8080"},
			want:  "8080",
		},
		{
			name:    "undefined before default collected",
			input:   "${UNSET_ONE} and ${UNSET_TWO:-fallback}",
			env:     map[string]string{},
			want:    " and fallback",
			wantErr: []string{"UNSET_ONE"},
		},
		{
			name:    "undefined after default collected",
			input:   "${A:-x}${UNSET_TWO}",
			env:     map[string]string{},
			want:    "x",
			wantErr: []string{"UNSET_TWO"},
		},
		{
			name:    "undefined before and after default both collected",
			input:   "${U1} ${A:-x} ${U2}",
			env:     map[string]string{},
			want:    " x ",
			wantErr: []string{"U1", "U2"},
		},
		{
			name:    "undefined variable used in non-default context",
			input:   "${UNSET_VAR:-}${USED_LATER}",
			env:     map[string]string{},
			want:    "",
			wantErr: []string{"USED_LATER"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expander := &envExpander{
				lookup: func(name string) (string, bool) {
					v, ok := tt.env[name]
					return v, ok
				},
			}
			got, missing := expander.expand(tt.input)
			if got != tt.want {
				t.Fatalf("expand(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !stringSlicesEqual(missing, tt.wantErr) {
				t.Fatalf("expand(%q) missing = %v, want %v", tt.input, missing, tt.wantErr)
			}
		})
	}
}

func TestExpandNodeTreeMapping(t *testing.T) {
	yamlContent := `providers:
  local:
    base_url: ${BASE_URL}
    api_key: ${API_KEY}
`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	missing := expandNodeTree(&root, func(name string) (string, bool) {
		if name == "API_KEY" {
			return "secret", true
		}
		return "", false
	})

	// Should report BASE_URL as undefined
	if len(missing) != 1 {
		t.Fatalf("expandNodeTree returned %d undefined vars, want 1", len(missing))
	}
	if missing[0].Name != "BASE_URL" {
		t.Fatalf("missing[0].Name = %q, want %q", missing[0].Name, "BASE_URL")
	}
	if !strings.Contains(missing[0].Path, "base_url") {
		t.Fatalf("missing[0].Path = %q, want to contain base_url", missing[0].Path)
	}

	// Verify the expanded tree can be marshalled back to YAML
	cleaned, err := yaml.Marshal(&root)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Check that the marshalled YAML contains the expanded secret value
	if !strings.Contains(string(cleaned), "secret") {
		t.Fatalf("marshalled YAML doesn't contain expanded secret: %s", cleaned)
	}
}

func TestExpandNodeTreeSequence(t *testing.T) {
	yamlContent := `servers:
  - url: ${URL1}
  - url: ${URL2}
`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	missing := expandNodeTree(&root, func(name string) (string, bool) {
		if name == "URL2" {
			return "http://example.com", true
		}
		return "", false
	})

	// Should report URL1 as undefined
	if len(missing) != 1 {
		t.Fatalf("expandNodeTree returned %d undefined vars, want 1", len(missing))
	}
	if missing[0].Name != "URL1" {
		t.Fatalf("missing[0].Name = %q, want %q", missing[0].Name, "URL1")
	}
	if !strings.Contains(missing[0].Path, "[0]") {
		t.Fatalf("missing[0].Path = %q, want to contain [0]", missing[0].Path)
	}
}

func TestExpandNodeTreeNeverExpandsKeys(t *testing.T) {
	yamlContent := `${KEY_NAME}: value`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	missing := expandNodeTree(&root, func(_ string) (string, bool) {
		return "expanded", true
	})

	// Keys should never be expanded, so no undefined vars and key unchanged
	if len(missing) != 0 {
		t.Fatalf("expandNodeTree returned %d undefined vars, want 0", len(missing))
	}

	// Verify the key is still unexpanded
	mappingNode := root.Content[0]
	if len(mappingNode.Content) < 2 {
		t.Fatal("mapping has no key-value pair")
	}
	keyNode := mappingNode.Content[0]
	if keyNode.Value != "${KEY_NAME}" {
		t.Fatalf("key was expanded to %q, want %q", keyNode.Value, "${KEY_NAME}")
	}
}

func TestExpandNodeTreeSkipsAliases(t *testing.T) {
	yamlContent := `anchor: &ref ${VAR}
reference: *ref
`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	missing := expandNodeTree(&root, func(_ string) (string, bool) {
		return "", false
	})

	// Should only report one undefined VAR (from the anchor), not from the alias
	wantCount := 1
	if len(missing) != wantCount {
		t.Fatalf("expandNodeTree returned %d undefined vars, want %d", len(missing), wantCount)
	}
}

func TestExpandNodeTreeEmptyFile(t *testing.T) {
	yamlContent := ""
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	missing := expandNodeTree(&root, func(_ string) (string, bool) {
		return "", false
	})

	if len(missing) != 0 {
		t.Fatalf("expandNodeTree returned %d undefined vars for empty file, want 0", len(missing))
	}
}

func TestUndefinedVarError(t *testing.T) {
	missing := []undefinedVar{
		{Path: "mcp.servers.github.headers.Authorization", Line: 42, Name: "GH_TOKEN"},
		{Path: "providers.openai.api_key", Line: 61, Name: "OPENAI_API_KEY"},
		{Path: "providers.openai.base_url", Line: 61, Name: "OPENAI_API_KEY"},
	}

	err := undefinedVarError("test.yaml", missing)
	errStr := err.Error()

	// Check that the error message contains expected content
	if !strings.Contains(errStr, "test.yaml") {
		t.Fatalf("error message missing config path: %v", err)
	}
	if !strings.Contains(errStr, "undefined environment variables") {
		t.Fatalf("error message missing descriptor: %v", err)
	}
	if !strings.Contains(errStr, "GH_TOKEN") {
		t.Fatalf("error message missing GH_TOKEN: %v", err)
	}
	if !strings.Contains(errStr, "OPENAI_API_KEY") {
		t.Fatalf("error message missing OPENAI_API_KEY: %v", err)
	}
	if !strings.Contains(errStr, "line 42") {
		t.Fatalf("error message missing line 42: %v", err)
	}
	if !strings.Contains(errStr, "line 61") {
		t.Fatalf("error message missing line 61: %v", err)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
