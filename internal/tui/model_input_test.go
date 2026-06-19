package tui

import (
	"testing"
)

func TestOneshotAllowedAction(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "/exit is allowed", input: "/exit", want: true},
		{name: "/thinking is allowed", input: "/thinking", want: true},
		{name: "/accent is allowed", input: "/accent", want: true},
		{name: "/accent amber is allowed", input: "/accent amber", want: true},
		{name: "/accent foo prefix is allowed", input: "/accent foo", want: true},
		{name: "/oneshot is NOT allowed", input: "/oneshot do something", want: false},
		{name: "hello world is NOT allowed", input: "hello world", want: false},
		{name: "leading whitespace allowed", input: "  /exit", want: true},
		{name: "trailing whitespace allowed", input: "/exit  ", want: true},
		{name: "empty string is NOT allowed", input: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := oneshotAllowedAction(tc.input)
			if got != tc.want {
				t.Errorf("oneshotAllowedAction(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestHandleEnterRoutesToSteerDuringOneshot(t *testing.T) {
	input := newModelInput()
	input.SetValue("hello")

	m := Model{
		oneshotRunning: true,
		oneshotSteerCh: make(chan string, 4),
		input:          input,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
		},
	}

	updated, cmd := m.handleEnter()
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("handleEnter() returned a non-nil cmd, want nil (steer returns nil)")
	}
	if !m.steerQueued {
		t.Fatal("steerQueued = false, want true")
	}
	select {
	case msg := <-m.oneshotSteerCh:
		if msg != "hello" {
			t.Fatalf("steer channel got %q, want %q", msg, "hello")
		}
	default:
		t.Fatal("steer channel empty, expected hello")
	}
}

func TestBuildSlashOverlayItemsAllowlistDuringOneshot(t *testing.T) {
	m := Model{oneshotRunning: true}
	items := m.buildSlashOverlayItems()
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	got := make([]string, len(items))
	for i, item := range items {
		got[i] = item.command
	}
	want := []string{"/exit", "/thinking", "/accent"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
