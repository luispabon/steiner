package mcp

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestDeclaredStates(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.MCPConfig
		want []ServerState
	}{
		{
			name: "no servers",
			cfg:  config.MCPConfig{},
			want: nil,
		},
		{
			name: "sorted order and default transport",
			cfg: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"zeta":  {Command: "zeta-bin"},
					"alpha": {Command: "alpha-bin", Transport: "stdio"},
				},
			},
			want: []ServerState{
				{Name: "alpha", Status: ServerStatusDisabled, Transport: "stdio"},
				{Name: "zeta", Status: ServerStatusDisabled, Transport: "stdio"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeclaredStates(tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DeclaredStates() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
