// Package main — huh boundary contract.
//
// This file documents the ownership boundary between cmd/steiner and
// internal/tui for any terminal-level interaction library (e.g. charmbracelet/huh).
//
// Ownership rules:
//
//  1. cmd/steiner owns runtime orchestration.
//     This includes the main event loop, channel wiring, signal handling, and
//     all calls into agent.Runner. Approval and confirmation prompts that
//     require synchronous terminal I/O (e.g. huh forms) must be initiated here,
//     not inside internal/tui.
//
//  2. internal/tui owns in-app UI state and presentation.
//     The TUI model (tui.App) drives the Bubble Tea program. It must not
//     block on terminal I/O outside the Bubble Tea event loop. If a huh form
//     needs to take over the terminal temporarily, cmd/steiner must pause the
//     Bubble Tea program before presenting the form and resume it afterward.
//
//  3. huh terminal interaction must be isolated.
//     Any charmbracelet/huh usage that requires raw terminal control (alternate
//     screen, cursor management) must be confined to a single call site in
//     cmd/steiner (e.g. approval.go or a dedicated huh_form.go). It must not
//     be called from within internal/tui update or view methods.
//
// Rationale: mixing huh's raw-terminal mode with Bubble Tea's event loop
// produces undefined terminal state. Keeping huh calls in cmd/steiner ensures
// they happen outside the Bubble Tea program lifecycle and can be reasoned
// about independently.
//
// TODO(stage-N): if approval UI grows beyond a simple yes/no, extract a
// dedicated huh form runner here (cmd/steiner/huh_form.go) so that
// approval.go stays focused on policy and this file stays focused on the
// boundary contract.
package main
