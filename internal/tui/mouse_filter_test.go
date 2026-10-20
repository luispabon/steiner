package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestClassifyMouse(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.MouseMsg
		want tea.Msg
	}{
		{
			name: "left click",
			msg:  tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft},
			want: mouseClickMsg{x: 3, y: 4},
		},
		{
			name: "right click is ignored",
			msg:  tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseRight},
		},
		{
			name: "wheel up",
			msg:  tea.MouseWheelMsg{X: 1, Y: 2, Button: tea.MouseWheelUp},
			want: mouseWheelMsg{direction: "up", x: 1, y: 2},
		},
		{
			name: "wheel down",
			msg:  tea.MouseWheelMsg{X: 1, Y: 2, Button: tea.MouseWheelDown},
			want: mouseWheelMsg{direction: "down", x: 1, y: 2},
		},
		{
			name: "horizontal wheel is ignored",
			msg:  tea.MouseWheelMsg{X: 1, Y: 2, Button: tea.MouseWheelLeft},
		},
		{
			name: "motion with left button held",
			msg:  tea.MouseMotionMsg{X: 5, Y: 6, Button: tea.MouseLeft},
			want: mouseMotionMsg{x: 5, y: 6},
		},
		{
			name: "motion without a button is ignored",
			msg:  tea.MouseMotionMsg{X: 5, Y: 6, Button: tea.MouseNone},
		},
		{
			name: "left release",
			msg:  tea.MouseReleaseMsg{X: 7, Y: 8, Button: tea.MouseLeft},
			want: mouseReleaseMsg{x: 7, y: 8},
		},
		{
			name: "right release is ignored",
			msg:  tea.MouseReleaseMsg{X: 7, Y: 8, Button: tea.MouseRight},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := classifyMouse(tt.msg)
			if tt.want == nil {
				if cmd != nil {
					t.Errorf("classifyMouse(%#v) returned a command, want nil", tt.msg)
				}
				return
			}
			if cmd == nil {
				t.Fatalf("classifyMouse(%#v) returned nil, want %#v", tt.msg, tt.want)
			}
			if got := cmd(); got != tt.want {
				t.Errorf("classifyMouse(%#v) message = %#v, want %#v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestShouldIgnoreLeakedMouseRunes(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		recentWheel bool
		want        bool
	}{
		{name: "empty text", text: "", recentWheel: true, want: false},
		{name: "complete SGR press is ignored", text: "[<0;10;5M", want: true},
		{name: "complete SGR release is ignored", text: "[<0;10;5m", want: true},
		{name: "bare bracket after wheel is ignored", text: "[", recentWheel: true, want: true},
		{name: "bare bracket without wheel is kept", text: "[", want: false},
		{name: "SGR opener after wheel is ignored", text: "[<", recentWheel: true, want: true},
		{name: "SGR opener without wheel is kept", text: "[<", want: false},
		{name: "ordinary text after wheel is kept", text: "hello", recentWheel: true, want: false},
		{name: "bracketed word after wheel is kept", text: "[hello", recentWheel: true, want: false},
		{name: "ordinary text is kept", text: "hello", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIgnoreLeakedMouseRunes(tea.KeyPressMsg{Text: tt.text}, tt.recentWheel)
			if got != tt.want {
				t.Errorf("shouldIgnoreLeakedMouseRunes(%q, %v) = %v, want %v", tt.text, tt.recentWheel, got, tt.want)
			}
		})
	}
}

func TestIsLeakedMouseFragment(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
		want     bool
	}{
		{name: "SGR press", fragment: "[<0;10;5M", want: true},
		{name: "SGR release", fragment: "[<0;10;5m", want: true},
		{name: "SGR without bracket", fragment: "<0;10;5M", want: true},
		{name: "bare body with suffix", fragment: "0;10;5M", want: true},
		{name: "partial SGR with two params", fragment: "[<7;12", want: true},
		{name: "partial SGR with one param", fragment: "[<0", want: true},
		{name: "partial without bracket", fragment: "<0;3", want: true},

		{name: "empty", fragment: "", want: false},
		{name: "leading space", fragment: " [<0;10;5M", want: false},
		{name: "trailing space", fragment: "[<0;10;5M ", want: false},
		{name: "ordinary word", fragment: "hello", want: false},
		{name: "word ending in m", fragment: "item", want: false},
		{name: "plain number", fragment: "12345", want: false},
		{name: "number ending in M", fragment: "42M", want: false},
		{name: "two numbers without suffix", fragment: "10;20", want: false},
		{name: "three numbers without suffix", fragment: "1;2;3", want: false},
		{name: "SGR with three params and no suffix", fragment: "[<0;10;5", want: false},
		{name: "SGR with unknown suffix", fragment: "[<0;10;5X", want: false},
		{name: "SGR with non-digit param", fragment: "[<a;10;5M", want: false},
		{name: "SGR with empty param", fragment: "[<0;;5M", want: false},
		{name: "SGR with four params", fragment: "[<0;10;5;7M", want: false},
		{name: "unknown bracket prefix", fragment: "[x0;10;5M", want: false},
		{name: "bracket only", fragment: "[", want: false},
		{name: "suffix only", fragment: "M", want: false},
		{name: "opener and suffix only", fragment: "[<M", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLeakedMouseFragment(tt.fragment); got != tt.want {
				t.Errorf("isLeakedMouseFragment(%q) = %v, want %v", tt.fragment, got, tt.want)
			}
		})
	}
}

func TestStripMouseSuffix(t *testing.T) {
	tests := []struct {
		name       string
		fragment   string
		wantBody   string
		wantSuffix string
	}{
		{name: "press suffix", fragment: "0;1;2M", wantBody: "0;1;2", wantSuffix: "M"},
		{name: "release suffix", fragment: "0;1;2m", wantBody: "0;1;2", wantSuffix: "m"},
		{name: "no suffix", fragment: "0;1;2", wantBody: "0;1;2", wantSuffix: ""},
		{name: "empty", fragment: "", wantBody: "", wantSuffix: ""},
		{name: "suffix only", fragment: "M", wantBody: "", wantSuffix: "M"},
		{name: "other trailing letter kept", fragment: "0;1;2X", wantBody: "0;1;2X", wantSuffix: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, suffix := stripMouseSuffix(tt.fragment)
			if body != tt.wantBody || suffix != tt.wantSuffix {
				t.Errorf("stripMouseSuffix(%q) = (%q, %q), want (%q, %q)", tt.fragment, body, suffix, tt.wantBody, tt.wantSuffix)
			}
		})
	}
}

func TestStripMousePrefix(t *testing.T) {
	tests := []struct {
		name       string
		fragment   string
		wantBody   string
		wantPrefix string
		wantOK     bool
	}{
		{name: "bracket and angle", fragment: "[<0;1", wantBody: "0;1", wantPrefix: "[<", wantOK: true},
		{name: "angle only", fragment: "<0;1", wantBody: "0;1", wantPrefix: "<", wantOK: true},
		{name: "no prefix", fragment: "0;1", wantBody: "0;1", wantPrefix: "", wantOK: true},
		{name: "empty", fragment: "", wantBody: "", wantPrefix: "", wantOK: true},
		{name: "unknown bracket prefix rejected", fragment: "[x", wantBody: "", wantPrefix: "", wantOK: false},
		{name: "bracket only rejected", fragment: "[", wantBody: "", wantPrefix: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, prefix, ok := stripMousePrefix(tt.fragment)
			if body != tt.wantBody || prefix != tt.wantPrefix || ok != tt.wantOK {
				t.Errorf("stripMousePrefix(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.fragment, body, prefix, ok, tt.wantBody, tt.wantPrefix, tt.wantOK)
			}
		})
	}
}

func TestMousePartsValid(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  bool
	}{
		{name: "three numbers", parts: []string{"0", "10", "5"}, want: true},
		{name: "single number", parts: []string{"7"}, want: true},
		{name: "empty part", parts: []string{"1", ""}, want: false},
		{name: "leading empty part", parts: []string{"", "2"}, want: false},
		{name: "only empty part", parts: []string{""}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mousePartsValid(tt.parts); got != tt.want {
				t.Errorf("mousePartsValid(%q) = %v, want %v", tt.parts, got, tt.want)
			}
		})
	}
}

func TestMouseFragmentShapeAllowed(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		suffix    string
		partCount int
		want      bool
	}{
		{name: "bracket prefix, one part, no suffix", prefix: "[<", partCount: 1, want: true},
		{name: "bracket prefix, two parts, no suffix", prefix: "[<", partCount: 2, want: true},
		{name: "bracket prefix, three parts, press", prefix: "[<", suffix: "M", partCount: 3, want: true},
		{name: "bracket prefix, three parts, release", prefix: "[<", suffix: "m", partCount: 3, want: true},
		{name: "bracket prefix, three parts, no suffix", prefix: "[<", partCount: 3, want: false},
		{name: "bracket prefix, two parts, with suffix", prefix: "[<", suffix: "M", partCount: 2, want: false},
		{name: "bracket prefix, four parts", prefix: "[<", suffix: "M", partCount: 4, want: false},
		{name: "angle prefix, three parts, press", prefix: "<", suffix: "M", partCount: 3, want: true},
		{name: "angle prefix, one part, with suffix", prefix: "<", suffix: "m", partCount: 1, want: false},
		{name: "no prefix, three parts, release", prefix: "", suffix: "m", partCount: 3, want: true},
		{name: "no prefix, three parts, no suffix", prefix: "", partCount: 3, want: false},
		{name: "no prefix, two parts, press", prefix: "", suffix: "M", partCount: 2, want: false},
		{name: "unknown prefix", prefix: "[x", suffix: "M", partCount: 3, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mouseFragmentShapeAllowed(tt.prefix, tt.suffix, tt.partCount); got != tt.want {
				t.Errorf("mouseFragmentShapeAllowed(%q, %q, %d) = %v, want %v", tt.prefix, tt.suffix, tt.partCount, got, tt.want)
			}
		})
	}
}

func TestIsLeakedMousePrefixFragment(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
		want     bool
	}{
		{name: "single bracket", fragment: "[", want: true},
		{name: "repeated brackets", fragment: "[[", want: true},
		{name: "SGR opener", fragment: "[<", want: true},
		{name: "empty", fragment: "", want: false},
		{name: "leading space", fragment: " [", want: false},
		{name: "trailing space", fragment: "[ ", want: false},
		{name: "bracket and letter", fragment: "[a", want: false},
		{name: "angle only", fragment: "<", want: false},
		{name: "opener with params", fragment: "[<0", want: false},
		{name: "opener followed by bracket", fragment: "[<[", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLeakedMousePrefixFragment(tt.fragment); got != tt.want {
				t.Errorf("isLeakedMousePrefixFragment(%q) = %v, want %v", tt.fragment, got, tt.want)
			}
		})
	}
}
