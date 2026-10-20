package mcp

import (
	"context"
	"testing"
)

func TestConnectSessionUnsupportedTransport(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		want      string
	}{
		{
			name:      "unknown transport",
			transport: "bogus",
			want:      `connect mcp server "srv": unsupported transport "bogus"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ServerSpec{Name: "srv", Transport: tt.transport}
			sess, err := connectSession(context.Background(), spec, nil, nil, nil, 0)
			if err == nil {
				t.Fatalf("connectSession() = %v, nil error; want %q", sess, tt.want)
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("connectSession() error = %q; want %q", got, tt.want)
			}
		})
	}
}
