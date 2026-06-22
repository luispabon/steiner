//go:build linux

package notify

import (
	"os/exec"
	"testing"
	"time"

	esnotify "github.com/esiqveland/notify"
)

func TestExpireTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero is sticky", 0, esnotify.ExpireTimeoutNever},
		{"negative is sticky", -5 * time.Second, esnotify.ExpireTimeoutNever},
		{"positive 5s", 5 * time.Second, 5 * time.Second},
		{"positive 100ms", 100 * time.Millisecond, 100 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expireTimeout(tc.in)
			if got != tc.want {
				t.Errorf("expireTimeout(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsWayland(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"unset", "", false},
		{"set", "wayland-0", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WAYLAND_DISPLAY", tc.value)
			got := isWayland()
			if got != tc.want {
				t.Errorf("isWayland() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFocusCommand(t *testing.T) {
	t.Run("wayland returns nil", func(t *testing.T) {
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		t.Setenv("WINDOWID", "12345")
		if got := focusCommand(); got != nil {
			t.Errorf("focusCommand() = %v, want nil on Wayland", got)
		}
	})

	t.Run("xdotool with WINDOWID", func(t *testing.T) {
		p, err := exec.LookPath("xdotool")
		if err != nil {
			t.Skip("xdotool not found in PATH")
		}
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "99")
		got := focusCommand()
		if got == nil {
			t.Fatal("focusCommand() = nil, want xdotool argv")
		}
		if got[0] != p || got[1] != "windowactivate" || got[2] != "99" {
			t.Errorf("focusCommand() = %v, want [%s windowactivate 99]", got, p)
		}
	})

	t.Run("wmctrl fallback when no WINDOWID", func(t *testing.T) {
		p, err := exec.LookPath("wmctrl")
		if err != nil {
			t.Skip("wmctrl not found in PATH")
		}
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "")
		got := focusCommand()
		if got == nil {
			t.Fatal("focusCommand() = nil, want wmctrl argv")
		}
		if got[0] != p || got[1] != "-a" || got[2] != "steiner" {
			t.Errorf("focusCommand() = %v, want [%s -a steiner]", got, p)
		}
	})

	t.Run("nil when neither tool found", func(t *testing.T) {
		// Only run this test when we can verify neither tool is present.
		_, xdotoolErr := exec.LookPath("xdotool")
		_, wmctrlErr := exec.LookPath("wmctrl")
		if xdotoolErr == nil || wmctrlErr == nil {
			t.Skip("xdotool or wmctrl present in PATH; cannot test nil case")
		}
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "")
		if got := focusCommand(); got != nil {
			t.Errorf("focusCommand() = %v, want nil", got)
		}
	})
}
