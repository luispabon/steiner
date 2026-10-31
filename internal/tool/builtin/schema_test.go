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
	lineNumbers, _ := props["line_numbers"].(map[string]any)
	desc, _ := lineNumbers["description"].(string)
	if !strings.Contains(desc, "Continuation lines of multiline matches have column 0.") {
		t.Errorf("line_numbers description = %q, want continuation column 0 note", desc)
	}
	if _, ok := props["offset"]; !ok {
		t.Error("missing offset property")
	}
}

func TestGrepSchemaDescriptionsAndBounds(t *testing.T) {
	props := schemaProperties(GrepSchema())
	if props == nil {
		t.Fatal("properties is nil")
	}

	typeProp, _ := props["type"].(map[string]any)
	if typeProp == nil {
		t.Fatal("missing type property")
	}
	if _, ok := typeProp["enum"]; ok {
		t.Error("type should list valid names in its description, not an enum")
	}
	desc, _ := typeProp["description"].(string)
	if want := strings.Join(grepTypeNames(), ", "); !strings.Contains(desc, want) {
		t.Errorf("type description = %q, want the sorted accepted names %q", desc, want)
	}
	for _, mention := range []string{"case-insensitive", "alias", "rejected"} {
		if !strings.Contains(desc, mention) {
			t.Errorf("type description = %q, want it to mention %q", desc, mention)
		}
	}

	globProp, _ := props["glob"].(map[string]any)
	globDesc, _ := globProp["description"].(string)
	for _, mention := range []string{"basename", "search root", "zero directories"} {
		if !strings.Contains(globDesc, mention) {
			t.Errorf("glob description = %q, want it to mention %q", globDesc, mention)
		}
	}

	multilineProp, _ := props["multiline"].(map[string]any)
	multilineDesc, _ := multilineProp["description"].(string)
	for _, mention := range []string{"(?ms)", "(?-m)"} {
		if !strings.Contains(multilineDesc, mention) {
			t.Errorf("multiline description = %q, want it to mention %q", multilineDesc, mention)
		}
	}

	for _, name := range []string{"context", "before_context", "after_context"} {
		prop, _ := props[name].(map[string]any)
		if prop == nil {
			t.Fatalf("missing %s property", name)
		}
		if got := prop["minimum"]; got != 0 {
			t.Errorf("%s minimum = %v, want 0", name, got)
		}
	}
	offsetProp, _ := props["offset"].(map[string]any)
	if offsetProp == nil {
		t.Fatal("missing offset property")
	}
	if _, ok := offsetProp["minimum"]; ok {
		t.Error("offset should not declare a minimum; negative values clamp to 0")
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

// TestBashSchema exercises the schema built from the 120-second fallback cap.
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
	mo, _ := props["max_output_bytes"].(map[string]any)
	if mo == nil {
		t.Fatal("missing max_output_bytes property")
	}
	if mo["default"] != 12288 || mo["maximum"] != 32768 {
		t.Errorf("max_output_bytes default/maximum = %v/%v, want 12288/32768", mo["default"], mo["maximum"])
	}
	ro, _ := props["raw_output"].(map[string]any)
	if ro == nil || ro["type"] != "boolean" {
		t.Errorf("raw_output property = %v, want boolean", ro)
	}
	if _, ok := props["max_output_chars"]; ok {
		t.Error("max_output_chars should not be in schema")
	}
}

func TestBashSchemaConfiguredCap(t *testing.T) {
	tests := []struct {
		name        string
		capSeconds  int
		wantDefault int
	}{
		{"cap above 120 keeps the 120s default", 300, defaultBashTimeoutSeconds},
		{"cap at 600 ceiling keeps the 120s default", 600, defaultBashTimeoutSeconds},
		{"cap equal to 120", 120, 120},
		{"cap below 120 becomes the default", 10, 10},
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
