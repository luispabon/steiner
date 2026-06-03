package interactive

import "testing"

func TestActiveRunControllerSteer(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "send and receive via Drain",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("hello")
				got := c.Drain()
				if got != "hello" {
					t.Errorf("Drain() = %q, want %q", got, "hello")
				}
			},
		},
		{
			name: "Drain returns empty string when no message pending",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				got := c.Drain()
				if got != "" {
					t.Errorf("Drain() = %q, want empty string", got)
				}
			},
		},
		{
			name: "latest-wins: send twice when full",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("first")
				c.Steer("second")
				got := c.Drain()
				if got != "second" {
					t.Errorf("Drain() = %q, want %q", got, "second")
				}
			},
		},
		{
			name: "Clear drains pending steer",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("pending")
				c.Clear()
				got := c.Drain()
				if got != "" {
					t.Errorf("after Clear(), Drain() = %q, want empty string", got)
				}
			},
		},
		{
			name: "SteerCh returns non-nil channel",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				ch := c.SteerCh()
				if ch == nil {
					t.Error("SteerCh() = nil, want non-nil channel")
				}
			},
		},
		{
			name: "SteerCh returns same channel",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				ch1 := c.SteerCh()
				ch2 := c.SteerCh()
				if ch1 != ch2 {
					t.Error("SteerCh() returns different channels on successive calls")
				}
			},
		},
		{
			name: "SteerCh has capacity 1",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				ch := c.SteerCh()
				if cap(ch) != 1 {
					t.Errorf("SteerCh() cap = %d, want 1", cap(ch))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}
