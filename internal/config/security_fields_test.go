package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSecurityFieldPatternsMatchPatchTags(t *testing.T) {
	root := reflect.TypeOf(configPatch{})
	for _, pattern := range securityFieldPatterns {
		if err := resolvePatchPath(root, pattern); err != nil {
			t.Errorf("pattern %q does not resolve against configPatch: %v", pattern, err)
		}
	}
}

func TestMCPServerPatchFieldsClassified(t *testing.T) {
	exceptions := map[string]bool{
		"enabled":         true,
		"connect_timeout": true,
		"allowed_tools":   true,
		"blocked_tools":   true,
		"sub_agents":      true,
	}
	const prefix = "mcp.servers.*."
	covered := map[string]bool{}
	for _, pattern := range securityFieldPatterns {
		if strings.HasPrefix(pattern, prefix) {
			covered[strings.TrimPrefix(pattern, prefix)] = true
		}
	}

	typ := reflect.TypeOf(mcpServerPatch{})
	for i := 0; i < typ.NumField(); i++ {
		tag := yamlTagName(typ.Field(i))
		if !covered[tag] && !exceptions[tag] {
			t.Errorf("mcpServerPatch field %q is neither security-classified nor in the exception set", tag)
		}
	}
}

func TestIsSecurityPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"sandbox.enabled", true},
		{"sandbox.host_mounts", true},
		{"paths.exclude_paths", false},
		{"mcp.servers.x.command", true},
		{"mcp.servers.x.enabled", false},
		{"mcp.servers.x.env.FOO", true},
		{"lsp.servers.go.command", true},
		{"lsp.servers.go.file_extensions", false},
		{"tools.t.exec", true},
		{"tools.t.description", false},
		{"providers.p.base_url", true},
		{"providers.p.timeout", false},
		{"models.default", false},
		{"mcp.servers", true},
		{"tui.fps", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isSecurityPath(tt.path); got != tt.want {
				t.Errorf("isSecurityPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// resolvePatchPath walks a dotted, "*"-wildcarded pattern against t's yaml
// tags and pointer/map layers, failing when any segment doesn't resolve to a
// real field or map element type.
func resolvePatchPath(t reflect.Type, path string) error {
	for _, seg := range strings.Split(path, ".") {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Map:
			if seg != "*" {
				return fmt.Errorf("segment %q: map type requires a %q wildcard", seg, "*")
			}
			t = t.Elem()
		case reflect.Struct:
			field, ok := fieldByYAMLTag(t, seg)
			if !ok {
				return fmt.Errorf("segment %q: no field with that yaml tag on %s", seg, t)
			}
			t = field.Type
		default:
			return fmt.Errorf("segment %q: unexpected type %s", seg, t)
		}
	}
	return nil
}

func fieldByYAMLTag(t reflect.Type, tag string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if yamlTagName(field) == tag {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func yamlTagName(field reflect.StructField) string {
	return strings.Split(field.Tag.Get("yaml"), ",")[0]
}
