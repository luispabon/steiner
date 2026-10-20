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
func (s *impl) Close(ctx context.Context) error {
	s.mux.Lock()
	defer s.mux.Unlock()

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

// abort tears down an unresponsive server and returns the originating cause.
func (s *impl) abort(cause error) error {
	s.connCancel()
	_ = s.conn.Close() // best-effort: the session is being torn down regardless
	s.proc.Kill()
	return cause
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
