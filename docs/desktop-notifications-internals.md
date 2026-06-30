# Desktop Notifications — Internals

User-facing documentation: [Desktop Notifications](desktop-notifications.md).

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
