package main

// huh_form.go — low-level helpers for running huh forms alongside the Bubble
// Tea program.
//
// Per the boundary contract in huh_boundary.go, all huh terminal interaction
// is isolated to cmd/steiner.  The Bubble Tea program must be paused before a
// huh form is presented and resumed immediately after, to avoid conflicting
// raw-terminal ownership.

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// approvalChoice is the result of a huh approval dialog.
type approvalChoice int

const (
	approvalChoiceAllow       approvalChoice = iota // allow this invocation
	approvalChoiceAlwaysAllow                       // allow all future invocations for this tool
	approvalChoiceDeny                              // deny this invocation
)

// runHuhApprovalForm pauses p, presents a huh Select dialog, then restores p.
// Returns the user's choice, or an error if the terminal could not be managed
// or if the user cancelled the form.
func runHuhApprovalForm(ctx context.Context, p *tea.Program, toolName, preview string) (approvalChoice, error) {
	if err := p.ReleaseTerminal(); err != nil {
		return approvalChoiceDeny, fmt.Errorf("release terminal: %w", err)
	}
	defer func() {
		_ = p.RestoreTerminal()
	}()

	choice := approvalChoiceAllow
	selectField := huh.NewSelect[approvalChoice]().
		Title(fmt.Sprintf("Tool approval: %s", toolName)).
		Description(truncatePreview(preview, 200)).
		Options(
			huh.NewOption("Allow once", approvalChoiceAllow),
			huh.NewOption("Always allow (this session)", approvalChoiceAlwaysAllow),
			huh.NewOption("Deny", approvalChoiceDeny),
		).
		Value(&choice)

	form := huh.NewForm(huh.NewGroup(selectField))
	if err := form.RunWithContext(ctx); err != nil {
		// User pressed ctrl-c / esc — treat as deny.
		return approvalChoiceDeny, nil
	}
	return choice, nil
}

// runHuhExitConfirmForm pauses p, presents a huh Confirm dialog asking whether
// to exit, then restores p.  Returns true if the user confirmed exit, false if
// they cancelled.  An error is returned only when terminal management fails.
func runHuhExitConfirmForm(ctx context.Context, p *tea.Program) (bool, error) {
	if err := p.ReleaseTerminal(); err != nil {
		return false, fmt.Errorf("release terminal: %w", err)
	}
	defer func() {
		_ = p.RestoreTerminal()
	}()

	confirmed := false
	confirmField := huh.NewConfirm().
		Title("Exit steiner?").
		Affirmative("Yes, exit").
		Negative("Cancel").
		Value(&confirmed)

	form := huh.NewForm(huh.NewGroup(confirmField))
	if err := form.RunWithContext(ctx); err != nil {
		// User pressed ctrl-c / esc — treat as cancel.
		return false, nil
	}
	return confirmed, nil
}

// truncatePreview shortens preview text to maxLen characters and appends "…"
// if truncated.
func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
