package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func runInteractiveSession(cmd *cobra.Command, sess *interactive.Session, p *tea.Program, rt *cliRuntime) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	wait := startInteractiveProgram(p, rt.events, stop)
	startModelCatalogRefresh(ctx, *rt, sess, rt.modelEntriesUpdates)
	if rt.mcpInit != nil {
		go rt.mcpInit.once.Do(func() { rt.mcpInit.run(ctx, *rt) })
	}
	err := sess.Run(ctx)
	stop()
	stopInteractiveProgram(p)
	wait()
	runQuitSequence(quitSequence{
		shutdownDelegation: func() { shutdownDelegation(context.Background(), rt, delegation.CancelCauseUser) },
		awaitRuns:          func() { awaitSessionRuns(cmd, sess, rt) },
		closeSession:       func() { closeSession(sess) },
		prune:              func() { pruneWorktreesOnExit(cmd, sess, rt) },
		farewell: func() {
			clearTerminalScreen(cmd.OutOrStdout())
			if err == nil && sess.SessionTitle() != "" {
				// best-effort: terminal write
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nResume this session:\n  steiner --resume %s\n\n", sess.SessionID())
			}
		},
		closeRuntime: func() { closeRuntime(rt) },
	})
	return err
}

// quitSequence holds the quit steps in the order they must run.
type quitSequence struct {
	shutdownDelegation func()
	awaitRuns          func()
	closeSession       func()
	prune              func()
	farewell           func()
	closeRuntime       func()
}

// runQuitSequence stops sub-agents first so tracked runs blocked on them can
// finish inside the drain window and the supervisor's cancelled results reach
// the driver; then the driver settles and saves; only then are unprotected
// worktrees pruned and the runtime closed.
func runQuitSequence(q quitSequence) {
	q.shutdownDelegation()
	q.awaitRuns()
	q.closeSession()
	q.prune()
	q.farewell()
	q.closeRuntime()
}

// sessionRunDrainTimeout bounds how long ordinary interactive shutdown waits for
// tracked session work (submitted prompts, steers, and prompt-history writes)
// before the runtime is closed. Prompt history is recorded on a tracked
// goroutine, so without this bounded wait the final write can be dropped on exit.
var sessionRunDrainTimeout = 5 * time.Second

// awaitSessionRuns gives tracked session work a bounded window to finish before
// the runtime is torn down. It always returns; on timeout it reports through the
// same session-health warning channel the rest of shutdown uses. A nil session is
// a no-op.
func awaitSessionRuns(cmd *cobra.Command, sess *interactive.Session, rt *cliRuntime) {
	if sess == nil {
		return
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), sessionRunDrainTimeout)
	finished := sess.WaitRuns(waitCtx)
	cancel()
	if finished {
		return
	}
	warning := errors.New("skipped because tracked session work was still finishing")
	if rt != nil && rt.events != nil {
		emitCloseWarning(rt.events, "session shutdown", warning)
		return
	}
	// best-effort: terminal write
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: session shutdown: %v.\n", warning)
}

// closeSession stops the session's conversation driver, cancelling a run that
// outlived the drain window and saving the settled conversation, bounded by the
// same window. A nil session is a no-op.
func closeSession(sess *interactive.Session) {
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionRunDrainTimeout)
	defer cancel()
	sess.Close(ctx)
}

// shutdownDelegation cancels every sub-agent and waits, bounded by the
// supervisor's join timeout, for them to stop. Children that do not stop are
// reported as a session-health warning and their worktrees stay protected from
// pruning. It is a no-op once the supervisor has shut down.
func shutdownDelegation(ctx context.Context, rt *cliRuntime, cause delegation.CancelCause) {
	if rt == nil || rt.delegationSupervisor == nil || rt.delegationSupervisor.Closed() {
		return
	}
	report := rt.delegationSupervisor.Shutdown(ctx, cause)
	if rt.events == nil {
		return
	}
	for _, child := range report.Unjoined {
		emitCloseWarning(rt.events, "sub-agent shutdown", fmt.Errorf("%s %q did not stop in time", child.AgentType, child.AgentID))
	}
}

// protectedWorktrees returns the worktrees of sub-agents that did not stop in time.
func protectedWorktrees(s *delegation.Supervisor) []string {
	if s == nil {
		return nil
	}
	return s.ProtectedWorktrees()
}

var worktreeCleanupJoinTimeout = 5 * time.Second

func pruneWorktreesOnExit(cmd *cobra.Command, sess *interactive.Session, rt *cliRuntime) {
	if rt == nil || rt.worktreeCleanup == nil || !rt.worktreeCleanup.ShouldPrune() {
		return
	}

	if sess != nil {
		joinCtx, cancel := context.WithTimeout(context.Background(), worktreeCleanupJoinTimeout)
		finished := sess.WaitRuns(joinCtx)
		cancel()
		if !finished {
			warning := errors.New("skipped because an active run was still finishing")
			if rt.events != nil {
				emitCloseWarning(rt.events, "worktree cleanup", warning)
			} else {
				// best-effort: terminal write
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: worktree cleanup: %v.\n", warning)
			}
			return
		}
	}
	pruneCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := rt.worktreeCleanup.Prune(pruneCtx)
	// best-effort: terminal write
	for _, path := range protectedWorktrees(rt.delegationSupervisor) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Kept %s: sub-agent did not stop in time.\n", path)
	}
	if err != nil {
		if rt.events != nil {
			emitCloseWarning(rt.events, "worktree cleanup", err)
		}
		return
	}
	if n > 0 {
		// best-effort: terminal write
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nCleaned up %d worktree(s).\n\n", n)
	}
}

func startInteractiveProgram(p *tea.Program, events output.EventSink, stop context.CancelFunc) func() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := runTeaProgram(p); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			emitInteractiveProgramWarning(events, err)
		}
		stop()
	}()
	return wg.Wait
}

func stopInteractiveProgram(p *tea.Program) {
	quitTeaProgram(p)
}

func emitInteractiveProgramWarning(events output.EventSink, err error) {
	events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "session_health",
		Severity: "warning",
		Notes:    []string{fmt.Sprintf("tui runtime failed: %v", err)},
	}))
}
