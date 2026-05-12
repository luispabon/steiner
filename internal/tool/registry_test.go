package tool

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestSubset(t *testing.T) {
	// Create a parent registry with multiple tools
	parentDefs := []ToolDef{
		{Name: "tool_a", Description: "Tool A", Approval: config.ApprovalModePrompt},
		{Name: "tool_b", Description: "Tool B", Approval: config.ApprovalModePrompt},
		{Name: "tool_c", Description: "Tool C", Approval: config.ApprovalModePrompt},
		{Name: "tool_d", Description: "Tool D", Approval: config.ApprovalModePrompt},
	}
	parent := NewRegistry(parentDefs...)

	tests := []struct {
		name             string
		include          []string
		exclude          []string
		approvalOverride config.ApprovalMode
		wantNames        []string
		wantApproval     config.ApprovalMode
	}{
		{
			name:             "basic include filtering",
			include:          []string{"tool_a", "tool_b"},
			exclude:          []string{},
			approvalOverride: "",
			wantNames:        []string{"tool_a", "tool_b"},
			wantApproval:     config.ApprovalModePrompt,
		},
		{
			name:             "exclude removes from include set",
			include:          []string{"tool_a", "tool_b", "tool_c"},
			exclude:          []string{"tool_b"},
			approvalOverride: "",
			wantNames:        []string{"tool_a", "tool_c"},
			wantApproval:     config.ApprovalModePrompt,
		},
		{
			name:             "empty include returns empty registry",
			include:          []string{},
			exclude:          []string{},
			approvalOverride: "",
			wantNames:        []string{},
			wantApproval:     "",
		},
		{
			name:             "approvalOverride applies to all included defs",
			include:          []string{"tool_a", "tool_c"},
			exclude:          []string{},
			approvalOverride: config.ApprovalModeAuto,
			wantNames:        []string{"tool_a", "tool_c"},
			wantApproval:     config.ApprovalModeAuto,
		},
		{
			name:             "empty approvalOverride leaves approval unchanged",
			include:          []string{"tool_a", "tool_d"},
			exclude:          []string{},
			approvalOverride: "",
			wantNames:        []string{"tool_a", "tool_d"},
			wantApproval:     config.ApprovalModePrompt,
		},
		{
			name:             "exclude name not in registry is harmless",
			include:          []string{"tool_a", "tool_b"},
			exclude:          []string{"tool_x"},
			approvalOverride: "",
			wantNames:        []string{"tool_a", "tool_b"},
			wantApproval:     config.ApprovalModePrompt,
		},
		{
			name:             "include name not in registry is silently ignored",
			include:          []string{"tool_a", "tool_x"},
			exclude:          []string{},
			approvalOverride: "",
			wantNames:        []string{"tool_a"},
			wantApproval:     config.ApprovalModePrompt,
		},
		{
			name:             "original registry unchanged after Subset",
			include:          []string{"tool_a"},
			exclude:          []string{},
			approvalOverride: config.ApprovalModeAuto,
			wantNames:        []string{"tool_a"},
			wantApproval:     config.ApprovalModeAuto,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subset := parent.Subset(tt.include, tt.exclude, tt.approvalOverride)

			// Verify names
			gotNames := subset.Names()
			if len(gotNames) != len(tt.wantNames) {
				t.Fatalf("got %d names, want %d: %v vs %v", len(gotNames), len(tt.wantNames), gotNames, tt.wantNames)
			}
			for i, name := range gotNames {
				if name != tt.wantNames[i] {
					t.Errorf("got name %s at index %d, want %s", name, i, tt.wantNames[i])
				}
			}

			// Verify approval mode for each tool
			if len(tt.wantNames) > 0 {
				for _, name := range gotNames {
					def, ok := subset.Get(name)
					if !ok {
						t.Errorf("tool %s not found in subset", name)
						continue
					}
					if tt.wantApproval != "" && def.Approval != tt.wantApproval {
						t.Errorf("tool %s has approval %v, want %v", name, def.Approval, tt.wantApproval)
					}
				}
			}

			// For the "original unchanged" test, verify parent was not mutated
			if tt.name == "original registry unchanged after Subset" {
				parentDef, _ := parent.Get("tool_a")
				if parentDef.Approval != config.ApprovalModePrompt {
					t.Errorf("parent approval was mutated to %v", parentDef.Approval)
				}
			}
		})
	}

	// Test nil receiver
	t.Run("nil receiver returns empty registry", func(t *testing.T) {
		var nilReg *Registry
		subset := nilReg.Subset([]string{"tool_a"}, []string{}, "")
		if subset == nil {
			t.Errorf("expected non-nil empty registry, got nil")
		}
		names := subset.Names()
		if len(names) != 0 {
			t.Errorf("nil receiver subset has %d names, want 0", len(names))
		}
	})
}
