//go:build linux

package notify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	esnotify "github.com/esiqveland/notify"
)

// fakeNotifier implements esnotify.Notifier for testing.
type fakeNotifier struct {
	lastNotification esnotify.Notification
	returnID         uint32
	returnErr        error
}

func (f *fakeNotifier) SendNotification(n esnotify.Notification) (uint32, error) {
	f.lastNotification = n
	return f.returnID, f.returnErr
}

func (f *fakeNotifier) GetCapabilities() ([]string, error) {
	return nil, nil
}

func (f *fakeNotifier) GetServerInformation() (esnotify.ServerInformation, error) {
	return esnotify.ServerInformation{}, nil
}

func (f *fakeNotifier) CloseNotification(_ uint32) (bool, error) {
	return false, nil
}

func (f *fakeNotifier) Close() error {
	return nil
}

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

func TestLinuxDriverNotify(t *testing.T) {
	tests := []struct {
		name          string
		duration      time.Duration
		returnID      uint32
		returnErr     error
		wantErr       bool
		expectTitle   string
		expectBody    string
		expectTimeout time.Duration
	}{
		{
			name:          "success with positive duration",
			duration:      5 * time.Second,
			returnID:      42,
			returnErr:     nil,
			wantErr:       false,
			expectTitle:   "steiner — myproject",
			expectBody:    "Build passed\nmain",
			expectTimeout: 5 * time.Second,
		},
		{
			name:          "zero duration becomes sticky",
			duration:      0,
			returnID:      1,
			returnErr:     nil,
			wantErr:       false,
			expectTitle:   "steiner — myproject",
			expectBody:    "Build passed\nmain",
			expectTimeout: esnotify.ExpireTimeoutNever,
		},
		{
			name:          "negative duration becomes sticky",
			duration:      -1 * time.Second,
			returnID:      2,
			returnErr:     nil,
			wantErr:       false,
			expectTitle:   "steiner — myproject",
			expectBody:    "Build passed\nmain",
			expectTimeout: esnotify.ExpireTimeoutNever,
		},
		{
			name:      "SendNotification error is wrapped",
			duration:  3 * time.Second,
			returnErr: fmt.Errorf("dbus error"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeNotifier{
				returnID:  tt.returnID,
				returnErr: tt.returnErr,
			}
			drv := &linuxDriver{
				appName:  "steiner",
				notifier: fake,
			}

			payload := Notification{
				Project: "myproject",
				Branch:  "main",
				Reason:  "Build passed",
			}

			err := drv.notify(context.Background(), payload, tt.duration)

			if (err != nil) != tt.wantErr {
				t.Errorf("notify() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				if err == nil || err.Error() != "send desktop notification: dbus error" {
					t.Errorf("notify() error = %v, want wrapped dbus error", err)
				}
			} else {
				if fake.lastNotification.AppName != "steiner" {
					t.Errorf("AppName = %q, want %q", fake.lastNotification.AppName, "steiner")
				}
				if fake.lastNotification.Summary != tt.expectTitle {
					t.Errorf("Summary = %q, want %q", fake.lastNotification.Summary, tt.expectTitle)
				}
				if fake.lastNotification.Body != tt.expectBody {
					t.Errorf("Body = %q, want %q", fake.lastNotification.Body, tt.expectBody)
				}
				if fake.lastNotification.ExpireTimeout != tt.expectTimeout {
					t.Errorf("ExpireTimeout = %v, want %v", fake.lastNotification.ExpireTimeout, tt.expectTimeout)
				}
				expectedActions := []esnotify.Action{esnotify.NewDefaultAction("Focus")}
				if !reflect.DeepEqual(fake.lastNotification.Actions, expectedActions) {
					t.Errorf("Actions = %+v, want %+v", fake.lastNotification.Actions, expectedActions)
				}
			}
		})
	}
}

func TestLinuxDriverAvailable(t *testing.T) {
	fake := &fakeNotifier{}
	drv := &linuxDriver{
		appName:  "steiner",
		notifier: fake,
	}

	avail, msg := drv.available()
	if !avail {
		t.Error("available() = false, want true")
	}
	if msg != "" {
		t.Errorf("available() message = %q, want %q", msg, "")
	}
}

func TestNewDriverWithoutDBusSession(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")

	drv := newDriver(Options{AppName: "steiner"})

	avail, msg := drv.available()
	if avail {
		t.Error("available() = true, want false")
	}
	if msg != "DBUS_SESSION_BUS_ADDRESS is not set" {
		t.Errorf("available() message = %q, want %q", msg, "DBUS_SESSION_BUS_ADDRESS is not set")
	}
}

// fakeBin creates a temporary directory with fake executable scripts.
// Returns the directory path.
func fakeBin(t *testing.T, names ...string) string {
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		script := "#!/bin/sh\necho \"$@\" >> \"$MARKER_FILE\"\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("failed to create fake binary %s: %v", name, err)
		}
	}
	return dir
}

func TestFocusCommand(t *testing.T) {
	t.Run("wayland returns nil", func(t *testing.T) {
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		t.Setenv("WINDOWID", "12345")
		t.Setenv("PATH", "")
		if got := focusCommand(); got != nil {
			t.Errorf("focusCommand() = %v, want nil on Wayland", got)
		}
	})

	t.Run("xdotool with WINDOWID", func(t *testing.T) {
		dir := fakeBin(t, "xdotool")
		t.Setenv("PATH", dir)
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "99")
		got := focusCommand()
		if got == nil {
			t.Fatal("focusCommand() = nil, want xdotool argv")
		}
		expectedPath := filepath.Join(dir, "xdotool")
		if got[0] != expectedPath || got[1] != "windowactivate" || got[2] != "99" {
			t.Errorf("focusCommand() = %v, want [%s windowactivate 99]", got, expectedPath)
		}
	})

	t.Run("wmctrl fallback when WINDOWID set but xdotool absent", func(t *testing.T) {
		dir := fakeBin(t, "wmctrl")
		t.Setenv("PATH", dir)
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "99")
		got := focusCommand()
		if got == nil {
			t.Fatal("focusCommand() = nil, want wmctrl argv")
		}
		expectedPath := filepath.Join(dir, "wmctrl")
		if got[0] != expectedPath || got[1] != "-a" || got[2] != "steiner" {
			t.Errorf("focusCommand() = %v, want [%s -a steiner]", got, expectedPath)
		}
	})

	t.Run("wmctrl when no WINDOWID", func(t *testing.T) {
		dir := fakeBin(t, "wmctrl")
		t.Setenv("PATH", dir)
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "")
		got := focusCommand()
		if got == nil {
			t.Fatal("focusCommand() = nil, want wmctrl argv")
		}
		expectedPath := filepath.Join(dir, "wmctrl")
		if got[0] != expectedPath || got[1] != "-a" || got[2] != "steiner" {
			t.Errorf("focusCommand() = %v, want [%s -a steiner]", got, expectedPath)
		}
	})

	t.Run("nil when neither tool found", func(t *testing.T) {
		dir := fakeBin(t)
		t.Setenv("PATH", dir)
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "")
		if got := focusCommand(); got != nil {
			t.Errorf("focusCommand() = %v, want nil", got)
		}
	})

	t.Run("empty PATH returns nil", func(t *testing.T) {
		t.Setenv("PATH", "")
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "")
		if got := focusCommand(); got != nil {
			t.Errorf("focusCommand() = %v, want nil with empty PATH", got)
		}
	})
}

func TestFocusTerminal(t *testing.T) {
	t.Run("invokes focusCommand and executes", func(t *testing.T) {
		dir := fakeBin(t, "xdotool")
		t.Setenv("PATH", dir)
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "12345")

		focusTerminal()

	})

	t.Run("empty PATH does not panic", func(t *testing.T) {
		t.Setenv("PATH", "")
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("WINDOWID", "")

		focusTerminal()

	})
}
