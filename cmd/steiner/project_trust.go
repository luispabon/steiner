package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tui"
)

// trustProjectConfigEnv is the environment escape hatch that trusts the
// current project's config for this run without prompting, mirroring
// --trust-project-config.
const trustProjectConfigEnv = "STEINER_TRUST_PROJECT_CONFIG"

// Test seams, mirroring the buildRuntime seam pattern in runtime.go.
var (
	isInteractiveTerminal = func() bool {
		return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stderr.Fd())
	}
	runTrustDialog  = tui.RunTrustDialog
	runNoticeDialog = tui.RunNoticeDialog
	inspectProject  = config.InspectProject
	trustProject    = config.TrustProject
	nowFunc         = time.Now
)

// envTruthy reports whether v is a truthy environment-variable value.
func envTruthy(v string) bool {
	return v == "1" || strings.EqualFold(v, "true")
}

// ensureProjectTrust resolves project config trust exactly once per process,
// in order: already-resolved (memoised) → stored trust → --trust-project-config
// flag → STEINER_TRUST_PROJECT_CONFIG env → interactive trust dialog → hard
// error. On success it records the decision on flags.
func ensureProjectTrust(cmd *cobra.Command, flags *cliFlags) error {
	if flags.trustResolved {
		return nil
	}

	opts := config.LoadOptions{CLI: config.CLIOverrides{ConfigPath: flags.configPath}}
	insp, err := inspectProject(opts)
	if err != nil {
		return fmt.Errorf("inspect project config: %w", err)
	}

	interactive := !flags.exec && isInteractiveTerminal()
	dio := tui.DialogIO{In: os.Stdin, Out: os.Stderr}

	if insp.StoreNotice != "" {
		if interactive {
			if err := runNoticeDialog(cmd.Context(), dio, "Trusted projects reset", insp.StoreNotice); err != nil {
				return fmt.Errorf("run notice dialog: %w", err)
			}
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), insp.StoreNotice)
		}
	}

	var source string
	switch {
	case insp.Trusted:
		source = "stored"
	case flags.trustProjectConfig:
		source = "flag"
		fmt.Fprintf(cmd.ErrOrStderr(), "trusting project %s for this run (--trust-project-config)\n", insp.ProjectRoot)
	case envTruthy(os.Getenv(trustProjectConfigEnv)):
		source = "env"
		fmt.Fprintf(cmd.ErrOrStderr(), "trusting project %s for this run (%s=1)\n", insp.ProjectRoot, trustProjectConfigEnv)
	case interactive:
		choice, err := runTrustDialog(cmd.Context(), dio, insp)
		if err != nil {
			return fmt.Errorf("run trust dialog: %w", err)
		}
		switch choice {
		case tui.TrustAlways:
			if err := trustProject(opts, insp.ProjectRoot, nowFunc()); err != nil {
				return fmt.Errorf("trust project: %w", err)
			}
			source = "always"
		case tui.TrustSession:
			source = "session"
		case tui.TrustDeny:
			return fmt.Errorf("project not trusted: %s", insp.ProjectRoot)
		}
	default:
		return fmt.Errorf("project %s is not trusted; run steiner interactively in it once to trust it, or pass --trust-project-config / set %s=1 for this run", insp.ProjectRoot, trustProjectConfigEnv)
	}

	flags.projectTrust = config.ProjectTrustTrusted
	flags.trustSource = source
	flags.projectRoot = insp.ProjectRoot
	flags.trustResolved = true
	return nil
}

// loadCLIConfig resolves project trust (once per process) and loads config.
func loadCLIConfig(cmd *cobra.Command, flags *cliFlags, cli config.CLIOverrides) (config.Config, error) {
	if err := ensureProjectTrust(cmd, flags); err != nil {
		return config.Config{}, err
	}
	return config.Load(config.LoadOptions{CLI: cli, ProjectTrust: flags.projectTrust})
}
