package main

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
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

func runInteractiveMode(cmd *cobra.Command, flags *cliFlags) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	sess, err := buildInteractiveSession(rt)
	if err != nil {
		return err
	}
	rt = buildInteractiveRuntime(rt, sess)
	tuiApp := buildInteractiveApp(cmd, flags, rt, sess)
	wireInteractiveRunner(rt, sess)
	sess.DisplaySink().Set(tuiApp.EventSink())

	p := tuiApp.NewProgram()
	defer tuiApp.Cleanup()
	if err := resumeInteractiveSession(cmd.Context(), sess, flags.resume, p, cmd.OutOrStdout(), &rt); err != nil {
		return err
	}
	return runInteractiveSession(cmd, sess, p, &rt)
}
