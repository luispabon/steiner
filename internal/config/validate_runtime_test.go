package config

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestValidateToolsConfigReservedNames(t *testing.T) {
	builtinNames := ReservedToolNames()
	validTool := ToolConfig{Exec: "/bin/true", Timeout: MustDuration("30s")}

	tests := []struct {
		name    string
		tools   map[string]ToolConfig
		wantErr []string
	}{
		{
			name: "each built-in name is rejected",
			tools: func() map[string]ToolConfig {
				m := make(map[string]ToolConfig, len(builtinNames))
				for _, n := range builtinNames {
					m[n] = validTool
				}
				return m
			}(),
			wantErr: func() []string {
				var msgs []string
				for _, n := range builtinNames {
					msgs = append(msgs, fmt.Sprintf("tools[%q]: name is reserved by a built-in tool", n))
				}
				return msgs
			}(),
		},
		{
			name:  "custom name is accepted",
			tools: map[string]ToolConfig{"my_tool": validTool},
		},
		{
			name:    "empty exec still reported",
			tools:   map[string]ToolConfig{"my_tool": {Timeout: MustDuration("30s")}},
			wantErr: []string{`tools["my_tool"].exec is required`},
		},
		{
			name:    "zero timeout still reported",
			tools:   map[string]ToolConfig{"my_tool": {Exec: "/bin/true"}},
			wantErr: []string{`tools["my_tool"].timeout must be greater than zero`},
		},
		{
			name:  "reserved name with missing exec and timeout reports all",
			tools: map[string]ToolConfig{"bash": {}},
			wantErr: []string{
				`tools["bash"]: name is reserved by a built-in tool`,
				`tools["bash"].exec is required`,
				`tools["bash"].timeout must be greater than zero`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateToolsConfig(&problems, tt.tools)
			// Map iteration order is unspecified, so compare sorted.
			sort.Strings(problems)
			want := append([]string(nil), tt.wantErr...)
			sort.Strings(want)
			if !reflect.DeepEqual(problems, want) {
				t.Fatalf("validateToolsConfig() problems = %v, want %v", problems, want)
			}
		})
	}
}
