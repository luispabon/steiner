package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// errModelCallTimeout and errTurnTimeout are context causes attached to the
// per-call and per-turn deadlines so they can be told apart from a genuine
// parent cancellation (Ctrl-C, run deadline).
var (
	errModelCallTimeout = errors.New("model call timeout")
	errTurnTimeout      = errors.New("turn timeout")
)

// isLimitTimeout reports whether ctx ended only because one of the runner's own
// per-call or per-turn deadlines fired.
func isLimitTimeout(ctx context.Context) bool {
	if ctx.Err() == nil {
		return false
	}
	cause := context.Cause(ctx)
	return errors.Is(cause, errModelCallTimeout) || errors.Is(cause, errTurnTimeout)
}

// limitTimeoutError converts an expired limit deadline into an error the
// transient-retry classifier accepts (a synthetic 504 gateway timeout).
func limitTimeoutError(ctx context.Context, timeout time.Duration) error {
	what := "model call"
	if errors.Is(context.Cause(ctx), errTurnTimeout) {
		what = "turn"
	}
	return fmt.Errorf("%s timed out after %s: %w", what, timeout, &provider.HTTPError{
		StatusCode: http.StatusGatewayTimeout,
		Status:     "504 Gateway Timeout (client-side deadline)",
	})
}

// limitTimeout returns the configured duration of whichever limit deadline
// expired on ctx.
func (p *turnProgressor) limitTimeout(ctx context.Context) time.Duration {
	if errors.Is(context.Cause(ctx), errTurnTimeout) {
		return p.request.Limits.TurnTimeout
	}
	return p.request.Limits.ModelCallTimeout
}

// recoverToolPanic converts a recovered tool panic into an ordinary tool error
// and records the stack for diagnostics.
func (p *turnProgressor) recoverToolPanic(turn int, call provider.ToolCall, r any) error {
	stack := debug.Stack()
	slog.Error("tool panicked", "turn", turn, "tool", call.Name, "call_id", call.ID, "panic", fmt.Sprint(r), "stack", string(stack))
	emitEvent(p.request.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn:     turn,
		Severity: "error",
		Message:  fmt.Sprintf("tool %q panicked: %v\n%s", call.Name, r, stack),
	}))
	return fmt.Errorf("tool %q panicked: %v", call.Name, r)
}
