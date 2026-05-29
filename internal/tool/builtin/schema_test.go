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
	s := BashSchema()
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
}
