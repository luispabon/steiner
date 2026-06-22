package interactive

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestActiveRunControllerSteer(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "send and receive via DrainSteers",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("hello", nil)
				got := c.DrainSteers()
				if len(got) != 1 || got[0].Text != "hello" {
					t.Errorf("DrainSteers() = %+v, want [{hello nil}]", got)
				}
			},
		},
		{
			name: "DrainSteers returns nil when no message pending",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				got := c.DrainSteers()
				if len(got) != 0 {
					t.Errorf("DrainSteers() = %+v, want empty", got)
				}
			},
		},
		{
			name: "FIFO ordering: DrainSteers returns all steers in order",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("first", nil)
				c.Steer("second", nil)
				c.Steer("third", nil)
				got := c.DrainSteers()
				if len(got) != 3 {
					t.Fatalf("DrainSteers() = %+v, want 3 items", got)
				}
				if got[0].Text != "first" || got[1].Text != "second" || got[2].Text != "third" {
					t.Errorf("DrainSteers() = %+v, want [first second third]", got)
				}
			},
		},
		{
			name: "Steer with images preserves them in DrainSteers",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				imgs := []agent.ImageBlock{{MediaType: "image/png", Data: "abc"}}
				c.Steer("see image", imgs)
				got := c.DrainSteers()
				if len(got) != 1 {
					t.Fatalf("DrainSteers() = %+v, want 1 item", got)
				}
				if got[0].Text != "see image" {
					t.Errorf("Text = %q, want %q", got[0].Text, "see image")
				}
				if len(got[0].Images) != 1 || got[0].Images[0].MediaType != "image/png" {
					t.Errorf("Images = %+v, want 1 png image", got[0].Images)
				}
			},
		},
		{
			name: "DrainSteers empties queue on second call",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("msg", nil)
				c.DrainSteers()
				got := c.DrainSteers()
				if len(got) != 0 {
					t.Errorf("second DrainSteers() = %+v, want empty", got)
				}
			},
		},
		{
			name: "Clear empties drain queue",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("pending", nil)
				c.Clear()
				got := c.DrainSteers()
				if len(got) != 0 {
					t.Errorf("after Clear(), DrainSteers() = %+v, want empty", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}
