package tool

import (
	"testing"
)

func TestRegistryClone_IndependentCopy(t *testing.T) {
	original := NewRegistry(ToolDef{
		Name:        "alpha",
		Description: "first tool",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x": map[string]any{"type": "string"},
			},
		},
	})

	clone := original.Clone()

	// clone has same tools
	if !containsName(clone, "alpha") {
		t.Fatal("clone missing alpha")
	}

	// mutate clone: add new tool
	clone.Register(ToolDef{Name: "beta", Description: "second tool"})
	if containsName(original, "beta") {
		t.Error("adding to clone leaked into original")
	}

	// mutate clone: mutate schema map
	def, ok := clone.Get("alpha")
	if !ok {
		t.Fatal("alpha not found in clone")
	}
	if props, ok2 := def.ParameterSchema["properties"].(map[string]any); ok2 {
		props["injected"] = "evil"
	}
	origDef, _ := original.Get("alpha")
	if props, ok2 := origDef.ParameterSchema["properties"].(map[string]any); ok2 {
		if _, found := props["injected"]; found {
			t.Error("schema mutation in clone leaked into original")
		}
	}
}

func TestRegistryClone_NilReceiver(t *testing.T) {
	var r *Registry
	clone := r.Clone()
	if clone == nil {
		t.Fatal("Clone of nil should return empty registry, not nil")
	}
	if len(clone.Names()) != 0 {
		t.Error("Clone of nil should return empty registry")
	}
}

func TestRegistryClone_EmptyRegistry(t *testing.T) {
	original := NewRegistry()
	clone := original.Clone()
	if clone == nil {
		t.Fatal("Clone returned nil")
	}
	clone.Register(ToolDef{Name: "new"})
	if containsName(original, "new") {
		t.Error("adding to empty clone leaked into original")
	}
}

func TestRegistryClone_PreservesAllFields(t *testing.T) {
	defs := []ToolDef{
		{Name: "t1", Description: "desc1"},
		{Name: "t2", Description: "desc2"},
		{Name: "t3", Description: "desc3"},
	}
	original := NewRegistry(defs...)
	clone := original.Clone()

	for _, d := range defs {
		if !containsName(clone, d.Name) {
			t.Errorf("clone missing tool %q", d.Name)
		}
	}
}

func containsName(r *Registry, name string) bool {
	for _, n := range r.Names() {
		if n == name {
			return true
		}
	}
	return false
}
