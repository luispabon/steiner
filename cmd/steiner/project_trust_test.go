package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tui"
)

func newTrustTestCommand(stderr *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetErr(stderr)
	return cmd
}

func withProjectTrustSeams(t *testing.T) {
	t.Helper()
	// TestMain sets this globally so unrelated tests don't hit the trust
	// prompt; clear it here so these tests exercise the real precedence.
	t.Setenv(trustProjectConfigEnv, "")
	oldInteractive := isInteractiveTerminal
	oldTrustDialog := runTrustDialog
	oldNoticeDialog := runNoticeDialog
	oldInspect := inspectProject
	oldTrust := trustProject
	oldNow := nowFunc
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		runTrustDialog = oldTrustDialog
		runNoticeDialog = oldNoticeDialog
		inspectProject = oldInspect
		trustProject = oldTrust
		nowFunc = oldNow
	})
}

func TestEnsureProjectTrust(t *testing.T) {
	t.Run("stored trusted", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj", Trusted: true}, nil
		}
		runTrustDialog = func(context.Context, tui.DialogIO, config.ProjectInspection) (tui.TrustChoice, error) {
			t.Fatal("trust dialog must not be called when already trusted")
			return tui.TrustDeny, nil
		}
		trustProject = func(config.LoadOptions, string, time.Time) error {
			t.Fatal("trustProject must not be called for stored trust")
			return nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("ensureProjectTrust() error = %v", err)
		}
		if flags.projectTrust != config.ProjectTrustTrusted {
			t.Fatalf("projectTrust = %v, want trusted", flags.projectTrust)
		}
		if flags.trustSource != "stored" {
			t.Fatalf("trustSource = %q, want stored", flags.trustSource)
		}
	})

	t.Run("trust flag", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}
		trustProject = func(config.LoadOptions, string, time.Time) error {
			t.Fatal("trustProject must not be called for the flag path")
			return nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{trustProjectConfig: true}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("ensureProjectTrust() error = %v", err)
		}
		if flags.trustSource != "flag" {
			t.Fatalf("trustSource = %q, want flag", flags.trustSource)
		}
		if !strings.Contains(stderr.String(), "--trust-project-config") {
			t.Fatalf("stderr = %q, want mention of --trust-project-config", stderr.String())
		}
	})

	t.Run("trust env true and TRUE", func(t *testing.T) {
		for _, val := range []string{"1", "TRUE"} {
			withProjectTrustSeams(t)
			isInteractiveTerminal = func() bool { return false }
			inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
				return config.ProjectInspection{ProjectRoot: "/proj"}, nil
			}
			trustProject = func(config.LoadOptions, string, time.Time) error {
				t.Fatal("trustProject must not be called for the env path")
				return nil
			}
			t.Setenv(trustProjectConfigEnv, val)

			var stderr bytes.Buffer
			cmd := newTrustTestCommand(&stderr)
			flags := &cliFlags{}
			if err := ensureProjectTrust(cmd, flags); err != nil {
				t.Fatalf("ensureProjectTrust() error = %v", err)
			}
			if flags.trustSource != "env" {
				t.Fatalf("val %q: trustSource = %q, want env", val, flags.trustSource)
			}
		}
	})

	t.Run("trust env 0 falls through to non-interactive error", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}
		t.Setenv(trustProjectConfigEnv, "0")

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		err := ensureProjectTrust(cmd, flags)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "--trust-project-config") || !strings.Contains(err.Error(), trustProjectConfigEnv) {
			t.Fatalf("error = %v, want mention of both escape hatches", err)
		}
	})

	t.Run("interactive trust always persists", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return true }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}
		var trustedRoot string
		trustProject = func(_ config.LoadOptions, root string, _ time.Time) error {
			trustedRoot = root
			return nil
		}
		runTrustDialog = func(context.Context, tui.DialogIO, config.ProjectInspection) (tui.TrustChoice, error) {
			return tui.TrustAlways, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("ensureProjectTrust() error = %v", err)
		}
		if trustedRoot != "/proj" {
			t.Fatalf("trustProject called with root = %q, want /proj", trustedRoot)
		}
		if flags.trustSource != "always" {
			t.Fatalf("trustSource = %q, want always", flags.trustSource)
		}
	})

	t.Run("interactive trust session does not persist", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return true }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}
		trustProject = func(config.LoadOptions, string, time.Time) error {
			t.Fatal("trustProject must not be called for a session trust")
			return nil
		}
		runTrustDialog = func(context.Context, tui.DialogIO, config.ProjectInspection) (tui.TrustChoice, error) {
			return tui.TrustSession, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("ensureProjectTrust() error = %v", err)
		}
		if flags.trustSource != "session" {
			t.Fatalf("trustSource = %q, want session", flags.trustSource)
		}
	})

	t.Run("interactive trust deny errors", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return true }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}
		runTrustDialog = func(context.Context, tui.DialogIO, config.ProjectInspection) (tui.TrustChoice, error) {
			return tui.TrustDeny, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		err := ensureProjectTrust(cmd, flags)
		if err == nil || !strings.Contains(err.Error(), "project not trusted:") {
			t.Fatalf("error = %v, want project not trusted", err)
		}
	})

	t.Run("non-interactive with no flag env or stored trust errors", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		err := ensureProjectTrust(cmd, flags)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "--trust-project-config") || !strings.Contains(err.Error(), trustProjectConfigEnv) {
			t.Fatalf("error = %v, want mention of both escape hatches", err)
		}
	})

	t.Run("exec flag forces non-interactive even on a tty", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return true }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj"}, nil
		}
		runTrustDialog = func(context.Context, tui.DialogIO, config.ProjectInspection) (tui.TrustChoice, error) {
			t.Fatal("trust dialog must not be called with --exec")
			return tui.TrustDeny, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{exec: true}
		err := ensureProjectTrust(cmd, flags)
		if err == nil || !strings.Contains(err.Error(), "--trust-project-config") {
			t.Fatalf("error = %v, want non-interactive error", err)
		}
	})

	t.Run("store notice interactive shown via notice dialog before trust dialog", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return true }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj", StoreNotice: "trust store was corrupt and reset"}, nil
		}
		var noticeCalledFirst, dialogCalled bool
		runNoticeDialog = func(context.Context, tui.DialogIO, string, string) error {
			noticeCalledFirst = !dialogCalled
			return nil
		}
		runTrustDialog = func(context.Context, tui.DialogIO, config.ProjectInspection) (tui.TrustChoice, error) {
			dialogCalled = true
			return tui.TrustSession, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("ensureProjectTrust() error = %v", err)
		}
		if !dialogCalled {
			t.Fatal("expected trust dialog to be called")
		}
		if !noticeCalledFirst {
			t.Fatal("expected notice dialog to run before the trust dialog")
		}
	})

	t.Run("store notice non-interactive printed to stderr without a dialog", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{ProjectRoot: "/proj", Trusted: true, StoreNotice: "trust store was corrupt and reset"}, nil
		}
		runNoticeDialog = func(context.Context, tui.DialogIO, string, string) error {
			t.Fatal("notice dialog must not be called when non-interactive")
			return nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("ensureProjectTrust() error = %v", err)
		}
		if !strings.Contains(stderr.String(), "trust store was corrupt and reset") {
			t.Fatalf("stderr = %q, want the store notice", stderr.String())
		}
	})

	t.Run("memoised: inspectProject called once across two invocations", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		calls := 0
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			calls++
			return config.ProjectInspection{ProjectRoot: "/proj", Trusted: true}, nil
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("first ensureProjectTrust() error = %v", err)
		}
		if err := ensureProjectTrust(cmd, flags); err != nil {
			t.Fatalf("second ensureProjectTrust() error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("inspectProject called %d times, want 1", calls)
		}
	})

	t.Run("inspect error is propagated", func(t *testing.T) {
		withProjectTrustSeams(t)
		isInteractiveTerminal = func() bool { return false }
		wantErr := errors.New("boom")
		inspectProject = func(config.LoadOptions) (config.ProjectInspection, error) {
			return config.ProjectInspection{}, wantErr
		}

		var stderr bytes.Buffer
		cmd := newTrustTestCommand(&stderr)
		flags := &cliFlags{}
		if err := ensureProjectTrust(cmd, flags); err == nil || !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want wrapping %v", err, wantErr)
		}
	})
}
