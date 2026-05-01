package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/tool"
)

func TestBuildChildToolRegistry(t *testing.T) {
	tests := []struct {
		name             string
		parentTools      []tool.ToolDef
		delegateToolName string
		wantToolCount    int
		wantExcludedName string
	}{
		{
			name: "excludes delegate tool",
			parentTools: []tool.ToolDef{
				{Name: "tool1"},
				{Name: "delegate"},
				{Name: "tool2"},
			},
			delegateToolName: "delegate",
			wantToolCount:    2,
			wantExcludedName: "delegate",
		},
		{
			name: "handles no match for delegate tool",
			parentTools: []tool.ToolDef{
				{Name: "tool1"},
				{Name: "tool2"},
			},
			delegateToolName: "nonexistent",
			wantToolCount:    2,
			wantExcludedName: "nonexistent",
		},
		{
			name:             "handles nil parent registry",
			parentTools:      nil,
			delegateToolName: "delegate",
			wantToolCount:    0,
			wantExcludedName: "delegate",
		},
		{
			name: "excludes only the named tool",
			parentTools: []tool.ToolDef{
				{Name: "read"},
				{Name: "write"},
				{Name: "delegate"},
				{Name: "delete"},
			},
			delegateToolName: "delegate",
			wantToolCount:    3,
			wantExcludedName: "delegate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var parent *tool.Registry
			if tt.parentTools != nil {
				parent = tool.NewRegistry(tt.parentTools...)
			}

			got := BuildChildToolRegistry(parent, tt.delegateToolName)

			names := got.Names()
			if len(names) != tt.wantToolCount {
				t.Errorf("child registry has %d tools, want %d: %v", len(names), tt.wantToolCount, names)
			}

			for _, name := range names {
				if name == tt.wantExcludedName {
					t.Errorf("child registry contains excluded tool %q", tt.wantExcludedName)
				}
			}
		})
	}
}
