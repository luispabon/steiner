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
