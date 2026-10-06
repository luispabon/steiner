package builtin

import (
	"testing"
)

func schemaType(s map[string]any) string {
	v, _ := s["type"].(string)
	return v
}

func schemaAdditionalProperties(s map[string]any) bool {
	v, _ := s["additionalProperties"].(bool)
	return v
}

func schemaRequired(s map[string]any) []string {
	r, _ := s["required"].([]string)
	return r
}

func schemaProperties(s map[string]any) map[string]any {
	p, _ := s["properties"].(map[string]any)
	return p
}

func TestReadSchema(t *testing.T) {
	s := ReadSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 1 || req[0] != "path" {
		t.Errorf("required = %v, want [path]", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if p, ok := props["path"]; ok {
		m, _ := p.(map[string]any)
		if m["type"] != "string" {
			t.Error("path.type should be string")
		}
	} else {
		t.Error("missing path property")
	}
	if p, ok := props["offset"]; ok {
		m, _ := p.(map[string]any)
		if m["type"] != "integer" {
			t.Error("offset.type should be integer")
		}
	} else {
		t.Error("missing offset property")
	}
	if p, ok := props["limit"]; ok {
		m, _ := p.(map[string]any)
		if m["type"] != "integer" {
			t.Error("limit.type should be integer")
		}
	} else {
		t.Error("missing limit property")
	}
}

func TestGlobSchema(t *testing.T) {
	s := GlobSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 1 || req[0] != "pattern" {
		t.Errorf("required = %v, want [pattern]", req)
	}
}

func TestGrepSchema(t *testing.T) {
	s := GrepSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 1 || req[0] != "pattern" {
		t.Errorf("required = %v, want [pattern]", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if _, ok := props["output_mode"]; !ok {
		t.Error("missing output_mode property")
	}
	if _, ok := props["context"]; !ok {
		t.Error("missing context property")
	}
	if _, ok := props["head_limit"]; !ok {
		t.Error("missing head_limit property")
	}
	if _, ok := props["offset"]; !ok {
		t.Error("missing offset property")
	}
}

func TestLSSchema(t *testing.T) {
	s := LSSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 0 {
		t.Errorf("required = %v, want []", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if _, ok := props["path"]; !ok {
		t.Error("missing path property")
	}
	if _, ok := props["recursive"]; !ok {
		t.Error("missing recursive property")
	}
	if _, ok := props["limit"]; !ok {
		t.Error("missing limit property")
	}
	if _, ok := props["offset"]; !ok {
		t.Error("missing offset property")
	}
}

func TestBashSchema(t *testing.T) {
	s := BashSchema(defaultBashTimeoutCapSeconds)
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 1 || req[0] != "command" {
		t.Errorf("required = %v, want [command]", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	ts, _ := props["timeout_seconds"].(map[string]any)
	if ts == nil {
		t.Fatal("missing timeout_seconds property")
	}
	if got := ts["default"]; got != defaultBashTimeoutSeconds {
		t.Errorf("timeout_seconds default = %v, want %d", got, defaultBashTimeoutSeconds)
	}
	if got := ts["maximum"]; got != defaultBashTimeoutCapSeconds {
		t.Errorf("timeout_seconds maximum = %v, want %d", got, defaultBashTimeoutCapSeconds)
	}
}

func TestBashSchemaConfiguredCap(t *testing.T) {
	tests := []struct {
		name        string
		capSeconds  int
		wantDefault int
	}{
		{"cap above 30 keeps the 30s default", 300, defaultBashTimeoutSeconds},
		{"cap equal to 30", 30, 30},
		{"cap below 30 becomes the default", 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := schemaProperties(BashSchema(tt.capSeconds))
			ts, _ := props["timeout_seconds"].(map[string]any)
			if ts == nil {
				t.Fatal("missing timeout_seconds property")
			}
			if got := ts["default"]; got != tt.wantDefault {
				t.Errorf("timeout_seconds default = %v, want %d", got, tt.wantDefault)
			}
			if got := ts["maximum"]; got != tt.capSeconds {
				t.Errorf("timeout_seconds maximum = %v, want %d", got, tt.capSeconds)
			}
		})
	}
}
