# Desktop notifications

Steiner can send a desktop notification when the interactive TUI is waiting for you, so you can step away during long tasks. Notifications are off by default and are sent only from the interactive TUI.

## When Steiner notifies

- A tool call needs approval (`Tool approval required: <tool>`).
- A workflow handoff is waiting for you to accept it (`workflow handoff requested`).

The title is `steiner` plus the project directory name, and the body shows the reason and the current git branch. Steiner does not notify when a run finishes.

## Setup

```yaml
desktop_notifications:
  enabled: true
  duration: 0   # requests a notification that stays until dismissed
```

A positive `duration` requests expiry after that many seconds. The desktop notification daemon controls delivery and dismissal. Field reference: [`desktop_notifications` block](configuration.md#desktop_notifications-block).

## Platform support

| Platform | Support |
|----------|---------|
| Linux | Supported through a freedesktop notification daemon on the D-Bus session bus, such as `dunst`, `mako`, or the one your desktop environment provides. |
| macOS | Not supported. |
| Windows | Not supported. |

On Linux X11, clicking a notification can raise the terminal window. On Wayland, clicks do not raise it.

## When notifications cannot be delivered

If notifications are enabled but unavailable at startup, for example on an unsupported platform or without a notification daemon, Steiner appends a `desktop notifications: <reason>` user message to the conversation. Notifications are skipped for that session.

Common reasons on Linux:

- `DBUS_SESSION_BUS_ADDRESS is not set`: no session bus is reachable, which is typical over SSH.
- `org.freedesktop.Notifications is not available` or `has no name owner`: no notification daemon is running.
