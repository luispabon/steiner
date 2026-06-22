# Desktop Notifications

Steiner can send native desktop notifications when the agent needs user input, letting you step away from the terminal while the agent runs long tasks.

## Behaviour

Desktop notifications are sent when:
- The agent requires approval for a tool call
- A workflow handoff is waiting for user acceptance

Notification delivery is best-effort — failures (missing daemon, headless environment, SSH session) never affect the agent loop. If enabled but undeliverable, Steiner emits a single non-fatal startup warning and silently no-ops for the remainder of the session.

## Configuration

The `desktop_notifications` block has two fields:

| Field      | Type | Default | Description |
|------------|------|---------|-------------|
| `enabled`  | bool | `false` | Master switch. Set to `true` to enable desktop notifications. |
| `duration` | int  | `0`     | Notification display duration in seconds. Set to `0` for persistent (sticky) notifications that do not auto-dismiss. Set to a positive integer to auto-dismiss after that many seconds. Must be >= 0. |

Example configuration:

```yaml
desktop_notifications:
  enabled: true
  duration: 0  # permanent (sticky) notifications
```

Auto-dismiss after 5 seconds:

```yaml
desktop_notifications:
  enabled: true
  duration: 5
```

For the complete configuration reference, see [docs/CONFIGURATION.md](CONFIGURATION.md).

## Platform support matrix

| Platform | Status | Details |
|----------|--------|---------|
| Linux | Supported | Notifications delivered via D-Bus / freedesktop notification daemon (e.g. `systemd-notify`, `notification-daemon`, `dunst`). Click-to-focus on X11 via best-effort xdotool/wmctrl. |
| macOS | Unsupported this iteration | Emits a single startup warning. Notifications are silently disabled for the session. No-op if enabled in config. |
| Windows | Unsupported this iteration | Emits a single startup warning. Notifications are silently disabled for the session. No-op if enabled in config. |

## Click-to-focus behavior

When a user clicks a desktop notification, Steiner attempts to focus its terminal window.

**X11 (Linux)**: Best-effort window raise using `xdotool` or `wmctrl`. Success depends on window manager behavior and available utilities.

**Wayland (Linux)**: No standard mechanism exists. Clicks on notifications are silently ignored — the notification service does not expose window activation to clients.

**macOS and Windows**: Not applicable (notifications disabled this iteration).

## Graceful degradation

When `desktop_notifications.enabled` is `true` but the notification system is unavailable:

1. **Startup check**: Steiner tests the notification daemon on startup.
2. **Single warning**: If unavailable (headless environment, SSH session, no daemon running), a non-fatal warning is shown in the TUI content area.
3. **Silent no-op**: For the remainder of the session, notification calls silently fail and execution continues.

This ensures that enabled-but-undeliverable notifications never stall the agent or disrupt the user experience.

## Adding a new platform driver

Desktop notification delivery is pluggable via build-tag drivers. Each platform implements a driver that registers with the notification service at startup.

### File structure

Create a new file `internal/notify/notify_<platform>.go` with the `//go:build <platform>` directive:

```go
//go:build linux

package notify

import "context"

func newDriver(opts Options) driver {
    // Return a driver implementation
    return &linuxDriver{...}
}
```

### Driver interface

Each driver must implement the unexported `driver` interface:

```go
type driver interface {
    // notify sends a notification with the given timeout duration.
    // ctx is the service context and should be respected for cancellation.
    // d is the display duration (0 = permanent/sticky).
    // Errors are logged but never fatal.
    notify(ctx context.Context, n Notification, d time.Duration) error

    // available reports whether this driver's platform supports notifications.
    // Returns (available, reason) where reason is a human-readable string
    // explaining why notifications are unavailable (e.g. "no D-Bus session",
    // "SSH session detected"). Used in startup checks and logging.
    available() (bool, string)
}
```

### Notification type

Drivers receive a `Notification` struct:

```go
type Notification struct {
    Project string // working directory basename, e.g. "myapp"
    Branch  string // current git branch, e.g. "main"
    Reason  string // human-readable trigger, e.g. "Tool approval required: bash"
}
```

### Integration

1. Implement `notify` and `available` on your driver.
2. Return the driver from `newDriver(opts Options) driver`.
3. The service automatically registers the driver at startup and calls `available()` to determine if notifications can be delivered.
4. If `available()` returns `false`, a single non-fatal warning is emitted and all future notify calls no-op gracefully.
