package lsp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Close shuts the server down gracefully: it sends shutdown, awaits the
// response, then sends exit. A server that does not exit within
// shutdownTimeout is force-terminated.
//
// The graceful sequence runs in its own goroutine because an LSP frame write is
// a blocking stream write: a server that stops draining its stdin wedges the
// write and no request deadline interrupts it. When the first caller's ctx ends
// before the sequence finishes, Close aborts: it terminates the process and
// closes the connection, which releases the stalled write, and joins that
// teardown before returning. The deadline therefore decides when the abort
// starts; the return happens once the released write and teardown finish.
//
// Close is idempotent. The first caller owns the sequence and always waits for
// it to finish; later callers get the same result, or their own context error if
// it ends first, while the owner still joins the teardown.
func (s *impl) Close(ctx context.Context) error {
	owner := false
	s.closeOnce.Do(func() {
		owner = true
		s.closeDone = make(chan struct{})
		go func() {
			defer close(s.closeDone)
			s.closeErr = s.shutdown(ctx)
		}()
	})

	if owner {
		<-s.closeDone
		return s.closeErr
	}

	select {
	case <-s.closeDone:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutdown runs the graceful sequence and aborts it when ctx ends before it
// finishes, so a stalled stream write cannot outlive the caller's deadline. The
// aborted teardown is joined before returning, so no goroutine outlives Close.
func (s *impl) shutdown(ctx context.Context) error {
	graceful := make(chan error, 1)
	go func() { graceful <- s.gracefulClose(ctx) }()

	var err error
	select {
	case err = <-graceful:
	case <-ctx.Done():
		// Terminating the process and closing the connection is what unblocks a
		// write the server will never drain; waiting for the graceful path alone
		// could block forever.
		s.abort(fmt.Errorf("shutdown interrupted: %w", ctx.Err())) //nolint:errcheck // the abort cause is the caller's deadline, reported below
		<-graceful
		err = ctx.Err()
	}

	s.awaitTeardown()
	return err
}

// gracefulClose is the shutdown/exit handshake. It may block on stream writes,
// so callers must bound it (see shutdown).
func (s *impl) gracefulClose(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return s.abort(fmt.Errorf("shutdown: %w", err))
	}
	if err := s.server.Exit(shutdownCtx); err != nil {
		return s.abort(fmt.Errorf("exit notification: %w", err))
	}

	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-s.proc.Exited():
		s.connCancel()
		_ = s.conn.Close() // best-effort: the peer usually drops the connection first
		return nil
	case <-timer.C:
		return s.abort(errors.New("server did not exit after shutdown"))
	}
}

// abort tears down an unresponsive server and returns the originating cause. The
// teardown runs once, on its own goroutine, and reports completion on abortDone:
// the process is killed first because a stream write blocked on a pipe the server
// no longer drains only returns once the peer is gone, and conn.Close waits for
// in-flight writes to retire. Callers must not wait on abortDone before the
// graceful sequence has returned.
func (s *impl) abort(cause error) error {
	s.abortOnce.Do(func() {
		s.abortDone = make(chan struct{})
		go func() {
			defer close(s.abortDone)
			s.proc.Kill()
			s.connCancel()
			_ = s.conn.Close() // best-effort: the session is being torn down regardless
		}()
	})
	return cause
}

// awaitTeardown waits for an aborted teardown to finish, if one started.
func (s *impl) awaitTeardown() {
	if s.abortDone != nil {
		<-s.abortDone
	}
}

// callWithExitCheck runs a request, failing fast if the server exits first.
func (s *impl) callWithExitCheck(ctx context.Context, fn func() (any, error)) (any, error) {
	if err := s.exitCheck(); err != nil {
		return nil, err
	}

	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)

	go func() {
		result, err := fn()
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-s.proc.Exited():
		return nil, errServerExited
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-done:
		return out.result, out.err
	}
}

// exitCheck reports errServerExited once the server is known to be gone, so a
// request issued after the exit never races the transport error into the caller.
func (s *impl) exitCheck() error {
	select {
	case <-s.proc.Exited():
		return errServerExited
	default:
		return nil
	}
}

// notifyWithExitCheck sends a notification, failing fast if the server exits first.
func (s *impl) notifyWithExitCheck(ctx context.Context, fn func() error) error {
	if err := s.exitCheck(); err != nil {
		return err
	}

	done := make(chan error, 1)

	go func() {
		done <- fn()
	}()

	select {
	case <-s.proc.Exited():
		return errServerExited
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}
