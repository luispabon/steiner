//go:build linux

package notify

import (
	"context"
	"os"
	"os/exec"
	"time"

	esnotify "github.com/esiqveland/notify"
	"github.com/godbus/dbus/v5"
)

type linuxDriver struct {
	opts Options
}

// newDriver returns a driver implementation for the current platform.
func newDriver(opts Options) driver {
	return &linuxDriver{opts: opts}
}

func (d *linuxDriver) available() (bool, string) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return false, "DBUS_SESSION_BUS_ADDRESS is not set"
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		return false, "cannot connect to D-Bus session bus: " + err.Error()
	}
	defer conn.Close() //nolint:errcheck

	var owner string
	obj := conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	call := obj.Call("org.freedesktop.DBus.GetNameOwner", 0, "org.freedesktop.Notifications")
	if call.Err != nil {
		return false, "org.freedesktop.Notifications is not available: " + call.Err.Error()
	}
	if err := call.Store(&owner); err != nil || owner == "" {
		return false, "org.freedesktop.Notifications has no name owner"
	}

	return true, ""
}

func (d *linuxDriver) notify(ctx context.Context, payload Notification, dur time.Duration) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		// non-fatal: best-effort delivery
		return nil
	}
	defer conn.Close() //nolint:errcheck

	var sentID uint32
	n, err := esnotify.New(conn,
		esnotify.WithOnAction(func(sig *esnotify.ActionInvokedSignal) {
			if sig.ID == sentID && sig.ActionKey == "default" {
				focusTerminal()
			}
		}),
	)
	if err != nil {
		// non-fatal
		return nil
	}
	defer n.Close() //nolint:errcheck

	esn := esnotify.Notification{
		AppName:       d.opts.AppName,
		Summary:       notificationTitle(payload),
		Body:          notificationBody(payload),
		ExpireTimeout: expireTimeout(dur),
		Actions: []esnotify.Action{
			esnotify.NewDefaultAction("Focus"),
		},
	}

	id, err := n.SendNotification(esn)
	if err != nil {
		// non-fatal
		return nil
	}
	sentID = id

	// Wait for context cancellation so the action handler can fire, then close.
	<-ctx.Done()

	return nil
}

// expireTimeout converts a duration to the D-Bus expire_timeout value.
// 0 means sticky (never expires). Positive values are passed through.
func expireTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return esnotify.ExpireTimeoutNever
	}
	return d
}

// isWayland reports whether the current session is running under Wayland.
func isWayland() bool {
	return os.Getenv("WAYLAND_DISPLAY") != ""
}

// focusCommand returns the argv to raise the terminal window, or nil if none available.
// Returns xdotool argv when WINDOWID is set and xdotool is found; falls back to wmctrl.
func focusCommand() []string {
	if isWayland() {
		return nil
	}
	windowID := os.Getenv("WINDOWID")
	if windowID != "" {
		if p, err := exec.LookPath("xdotool"); err == nil {
			return []string{p, "windowactivate", windowID}
		}
	}
	if p, err := exec.LookPath("wmctrl"); err == nil {
		return []string{p, "-a", "steiner"}
	}
	return nil
}

// focusTerminal attempts to raise the terminal window. All failures are swallowed.
func focusTerminal() {
	argv := focusCommand()
	if argv == nil {
		return
	}
	_ = exec.CommandContext(context.Background(), argv[0], argv[1:]...).Run()
}
