package main

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/tui"
)

const terminalClearSequence = "\x1b[2J\x1b[H"

var runTeaProgram = func(p *tea.Program) (tea.Model, error) {
	return p.Run()
}

var quitTeaProgram = func(p *tea.Program) {
	p.Quit()
}

func clearTerminalScreen(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, terminalClearSequence)
}

// interactiveProgramOptions returns the renderer options for the interactive
// program. The frame rate bounds how long a processed keystroke waits before
// it is flushed to the terminal, so it is user-visible; it is validated to
// 1-120 by config validation, and a zero value (config bypassed in tests)
// falls through to Bubble Tea's own default.
func interactiveProgramOptions(cfg config.Config) []tea.ProgramOption {
	if cfg.TUI.FPS < 1 {
		return nil
	}
	return []tea.ProgramOption{tea.WithFPS(cfg.TUI.FPS)}
}

func runInteractiveMode(cmd *cobra.Command, flags *cliFlags) error {
	// Paint the TUI before MCP servers finish connecting; the interactive
	// session runner waits for every server before the first agent turn.
	flags.asyncMCP = true
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	sess, err := buildInteractiveSession(rt)
	if err != nil {
		return err
	}
	rt = buildInteractiveRuntime(rt, sess)
	rt.worktreeCleanup = tui.NewWorktreeCleanupPlan(
		func() (int, error) {
			worktrees, err := delegation.ListProcessCodeWorktrees(context.Background(), rt.projectRoot)
			return len(worktrees), err
		},
		func(ctx context.Context) (int, error) {
			return delegation.PruneProcessCodeWorktrees(ctx, rt.projectRoot)
		},
	)
	tuiApp := buildInteractiveApp(cmd, flags, rt, sess)
	defer tuiApp.Cleanup()
	wireInteractiveRunner(rt, sess)
	sess.DisplaySink().Set(tuiApp.EventSink())
	if err := resumeInteractiveSession(cmd.Context(), sess, flags.resume, &rt); err != nil {
		return err
	}
	// The persisted mode is restored now; seed the model so the sidebar/footer and toggle match the session.
	tuiApp.SetInitialMode(string(sess.Mode()))
	p := tuiApp.NewProgram(interactiveProgramOptions(rt.cfg)...)
	return runInteractiveSession(cmd, sess, p, &rt)
}
