package builtin

import (
	"strings"
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

func TestWriteSchema(t *testing.T) {
	s := WriteSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 2 || req[0] != "path" || req[1] != "content" {
		t.Errorf("required = %v, want [path content]", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if _, ok := props["path"]; !ok {
		t.Error("missing path property")
	}
	if _, ok := props["content"]; !ok {
		t.Error("missing content property")
	}
	if len(props) != 2 {
		t.Errorf("expected 2 properties, got %d", len(props))
	}
}

func TestEditSchema(t *testing.T) {
	s := EditSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 3 || req[0] != "path" || req[1] != "old_string" || req[2] != "new_string" {
		t.Errorf("required = %v, want [path old_string new_string]", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if _, ok := props["path"]; !ok {
		t.Error("missing path property")
	}
	if _, ok := props["old_string"]; !ok {
		t.Error("missing old_string property")
	}
	if _, ok := props["new_string"]; !ok {
		t.Error("missing new_string property")
	}
	if _, ok := props["replace_all"]; ok {
		p, _ := props["replace_all"].(map[string]any)
		if p["type"] != "boolean" {
			t.Error("replace_all.type should be boolean")
		}
	} else {
		t.Error("missing replace_all property")
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

func TestApplyPatchSchema(t *testing.T) {
	s := ApplyPatchSchema()
	if got := schemaType(s); got != "object" {
		t.Errorf("type = %q, want %q", got, "object")
	}
	if schemaAdditionalProperties(s) {
		t.Error("additionalProperties should be false")
	}
	req := schemaRequired(s)
	if len(req) != 1 || req[0] != "patch" {
		t.Errorf("required = %v, want [patch]", req)
	}
	props := schemaProperties(s)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if len(props) != 2 {
		t.Fatalf("properties len = %d, want 2", len(props))
	}
	if _, ok := props["path"]; ok {
		t.Fatal("unexpected path property")
	}
	if _, ok := props["hunks"]; ok {
		t.Fatal("unexpected hunks property")
	}
	if _, ok := props["fuzzy_threshold"]; ok {
		t.Fatal("unexpected fuzzy_threshold property")
	}
	if p, ok := props["patch"]; ok {
		m, _ := p.(map[string]any)
		if m["type"] != "string" {
			t.Error("patch.type should be string")
		}
		if desc, _ := m["description"].(string); !strings.Contains(desc, "Use apply_patch for all file mutations.") || !strings.Contains(desc, "*** Begin Patch") || !strings.Contains(desc, "*** End Patch") {
			t.Fatalf("patch.description = %q, want apply_patch patch-format guidance", desc)
		}
	} else {
		t.Fatal("missing patch property")
	}
	if p, ok := props["dry_run"]; ok {
		m, _ := p.(map[string]any)
		if m["type"] != "boolean" {
			t.Error("dry_run.type should be boolean")
		}
	} else {
		t.Fatal("missing dry_run property")
	}
}
