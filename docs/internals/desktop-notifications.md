# Desktop notifications internals

User guide: [Desktop Notifications](../user/desktop-notifications.md).

## Service

`notify.New` calls `newDriver(opts)` directly; there is no driver registry. A disabled config uses `stubDriver`. `Notify` returns the driver's error. `notifyBlocking` (`internal/tui/model_events.go`) runs `Notify` in a goroutine and discards the error with `_ =`, so delivery failures are not logged. It is called from `internal/tui/model_events_approval.go` and `internal/tui/model_events_workflow.go`.

Only an approval request that opens the tray notifies, and the tray opens only while no other approval is active and `approvalRequestPromptable` accepts the request. When the controller implements `ApprovalHeadIdentity`, the request needs a non-empty call ID equal to the coordinator's FIFO head. Cached session grants emit `approval_requested` without registering with the `ApprovalCoordinator` (`approvalResponder.RequestApproval` in `internal/interactive/wiring.go`), so their call ID never matches the head and they neither open the tray nor notify; requests queued behind the head stay silent. Without a coordinating controller the event stream drives the tray and every request notifies. Decision events never notify: `applyApprovalEvent` leaves a still-headed active tray and its selection in place when the decision belongs to another call, such as a cached grant, and otherwise clears it, while `promoteNextApproval` reopens the next pending prompt silently. Workflow handoffs notify from `internal/tui/model_events_workflow.go` on every requested event, even while the modal is already open.

`Availability()` is read by the TUI at init (`internal/tui/model_init.go`), not by the service. The notifier is always built (`cmd/steiner/interactive_session.go`), so a disabled config still shows a `desktop notifications: <reason>` line.

## Notification content

The service is built with `AppName: "steiner"` (`cmd/steiner/interactive_session.go`). `expireTimeout` maps a duration of zero or less to `ExpireTimeoutNever`. Trigger strings are `Tool approval required: <tool>` and `workflow handoff requested`. `Project` is the base name of the working directory; `Branch` is the sidebar branch.

## Driver interface

`driver` (`internal/notify/notify.go`) has `notify(ctx, Notification, time.Duration) error` and `available() (bool, string)`. `Notification` carries `Project`, `Branch`, and `Reason`.

Linux (`notify_linux.go`, `//go:build linux`): `newDriver` checks `DBUS_SESSION_BUS_ADDRESS`, connects to the session bus, and checks `GetNameOwner` for `org.freedesktop.Notifications`. Each failure returns `stubDriver` with a reason. The notification's `Focus` default action calls `focusTerminal`: it is skipped on Wayland (`WAYLAND_DISPLAY`); otherwise it runs `xdotool windowactivate` using `WINDOWID` if found, else `wmctrl -a steiner`. Failures are ignored, and `focusTimeout` bounds the command.

Non-Linux (`notify_unsupported.go`, `//go:build !linux`): `newDriver` returns `stubDriver`.

## Adding a platform driver

Add `internal/notify/notify_<platform>.go` with a `//go:build <platform>` constraint, and exclude that platform from the `notify_unsupported.go` constraint. Implement `newDriver(opts Options) driver` and both methods. No registration is needed.
