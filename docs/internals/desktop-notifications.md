# Desktop notifications internals

User-facing documentation: [Desktop notifications](../user/desktop-notifications.md).

## Service

`notify.New` calls `newDriver(opts)` directly; there is no driver registry. A disabled config uses `stubDriver`. `Notify` returns the driver's error. The TUI calls `notifyBlocking` (`internal/tui/model_events_approval.go`, `internal/tui/model_events_workflow.go`), which runs `Notify` in a goroutine and discards the error with `_ =`, so delivery failures are not logged.

`Availability()` is read by the TUI at init (`internal/tui/model_init.go`), not by the service. The notifier is always built (`cmd/steiner/interactive_session.go`), so a disabled config still shows a `desktop notifications: <reason>` line.

## Configuration and triggers

`desktop_notifications.enabled` and `desktop_notifications.duration` (seconds, non-negative) configure the service, which is built with `AppName: "steiner"`. `expireTimeout` maps a duration of zero or less to `ExpireTimeoutNever`. Triggers are tool approval (`Tool approval required: <tool>`) and workflow handoff (`workflow handoff requested`). `Project` is the base name of the working directory; `Branch` is the sidebar branch.

## Driver interface

`driver` (`internal/notify/notify.go`) has `notify(ctx, Notification, time.Duration) error` and `available() (bool, string)`. `Notification` carries `Project`, `Branch`, and `Reason`.

Linux (`notify_linux.go`, `//go:build linux`): `newDriver` checks `DBUS_SESSION_BUS_ADDRESS`, connects to the session bus, and checks `GetNameOwner` for `org.freedesktop.Notifications`. Each failure returns `stubDriver` with a reason. The notification's `Focus` default action calls `focusTerminal`: it is skipped on Wayland (`WAYLAND_DISPLAY`); otherwise it runs `xdotool windowactivate` using `WINDOWID` if found, else `wmctrl -a steiner`. Failures are ignored, and `focusTimeout` bounds the command.

Non-Linux (`notify_unsupported.go`, `//go:build !linux`): `newDriver` returns `stubDriver`.

## Adding a platform driver

Add `internal/notify/notify_<platform>.go` with a `//go:build <platform>` constraint, and exclude that platform from the `notify_unsupported.go` constraint. Implement `newDriver(opts Options) driver` and both methods. No registration is needed.
