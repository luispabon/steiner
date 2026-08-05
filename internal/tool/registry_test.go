package tool

import (
	"testing"
)

func TestSubset(t *testing.T) {
	// Create a parent registry with multiple tools
	parentDefs := []ToolDef{
		{Name: "tool_a", Description: "Tool A"},
		{Name: "tool_b", Description: "Tool B"},
		{Name: "tool_c", Description: "Tool C"},
		{Name: "tool_d", Description: "Tool D"},
	}
	parent := NewRegistry(parentDefs...)

	tests := []struct {
		name      string
		include   []string
		exclude   []string
		wantNames []string
	}{
		{
			name:      "basic include filtering",
			include:   []string{"tool_a", "tool_b"},
			exclude:   []string{},
			wantNames: []string{"tool_a", "tool_b"},
		},
		{
			name:      "exclude removes from include set",
			include:   []string{"tool_a", "tool_b", "tool_c"},
			exclude:   []string{"tool_b"},
			wantNames: []string{"tool_a", "tool_c"},
		},
		{
			name:      "empty include returns empty registry",
			include:   []string{},
			exclude:   []string{},
			wantNames: []string{},
		},
		{
			name:      "exclude name not in registry is harmless",
			include:   []string{"tool_a", "tool_b"},
			exclude:   []string{"tool_x"},
			wantNames: []string{"tool_a", "tool_b"},
		},
		{
			name:      "include name not in registry is silently ignored",
			include:   []string{"tool_a", "tool_x"},
			exclude:   []string{},
			wantNames: []string{"tool_a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subset := parent.Subset(tt.include, tt.exclude)

			gotNames := subset.Names()
			if len(gotNames) != len(tt.wantNames) {
				t.Fatalf("got %d names, want %d: %v vs %v", len(gotNames), len(tt.wantNames), gotNames, tt.wantNames)
			}
			for i, name := range gotNames {
				if name != tt.wantNames[i] {
					t.Errorf("got name %s at index %d, want %s", name, i, tt.wantNames[i])
				}
			}
		})
	}

	// Test nil receiver
	t.Run("nil receiver returns empty registry", func(t *testing.T) {
		var nilReg *Registry
		subset := nilReg.Subset([]string{"tool_a"}, []string{})
		if subset == nil {
			t.Errorf("expected non-nil empty registry, got nil")
		}
		names := subset.Names()
		if len(names) != 0 {
			t.Errorf("nil receiver subset has %d names, want 0", len(names))
		}
	})

	// Verify parent registry is not mutated by Subset
	t.Run("original registry unchanged after Subset", func(t *testing.T) {
		subset := parent.Subset([]string{"tool_a"}, []string{})
		if len(subset.Names()) != 1 {
			t.Fatalf("subset has %d tools, want 1", len(subset.Names()))
		}
		// Parent should still have all 4 tools
		if len(parent.Names()) != 4 {
			t.Errorf("parent was mutated: has %d tools, want 4", len(parent.Names()))
		}
	})
}

func TestMCPProvenance(t *testing.T) {
	prov := MCPProvenance{Server: "fixture", ToolName: "echo"}
	mcpDef := ToolDef{
		Name:            "mcp__fixture__echo",
		Description:     "echoes text",
		ParameterSchema: map[string]any{"type": "object"},
		MCP:             prov,
	}
	builtin := ToolDef{Name: "read", Description: "reads a file"}

	// Built-in tools carry the zero value of MCPProvenance.
	if builtin.MCP != (MCPProvenance{}) {
		t.Fatalf("built-in MCP = %+v, want zero value", builtin.MCP)
	}

	reg := NewRegistry(mcpDef, builtin)

	// Get preserves provenance.
	got, ok := reg.Get(mcpDef.Name)
	if !ok {
		t.Fatal("Get() = false, want true")
	}
	if got.MCP != prov {
		t.Errorf("Get() MCP = %+v, want %+v", got.MCP, prov)
	}
	if gotBuiltin, ok := reg.Get("read"); ok && gotBuiltin.MCP != (MCPProvenance{}) {
		t.Errorf("Get() built-in MCP = %+v, want zero value", gotBuiltin.MCP)
	}

	// Definitions preserves provenance.
	found := false
	for _, d := range reg.Definitions() {
		if d.Name == mcpDef.Name {
			found = true
			if d.MCP != prov {
				t.Errorf("Definitions() MCP = %+v, want %+v", d.MCP, prov)
			}
		}
	}
	if !found {
		t.Fatal("Definitions() missing MCP tool")
	}

	// Clone preserves provenance.
	got, ok = reg.Clone().Get(mcpDef.Name)
	if !ok {
		t.Fatal("Clone().Get() = false, want true")
	}
	if got.MCP != prov {
		t.Errorf("Clone().Get() MCP = %+v, want %+v", got.MCP, prov)
	}

	// Subset preserves provenance.
	got, ok = reg.Subset([]string{mcpDef.Name}, nil).Get(mcpDef.Name)
	if !ok {
		t.Fatal("Subset().Get() = false, want true")
	}
	if got.MCP != prov {
		t.Errorf("Subset().Get() MCP = %+v, want %+v", got.MCP, prov)
	}
}
