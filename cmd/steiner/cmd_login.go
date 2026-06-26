package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/oauth"
)

func newLoginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with supported providers",
	}
	cmd.AddCommand(newLoginCodexCommand())
	return cmd
}

func newLoginCodexCommand() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Authenticate with OpenAI Codex via OAuth",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := oauth.DefaultTokenPath()
			if err != nil {
				return fmt.Errorf("get token path: %w", err)
			}

			store := oauth.NewTokenStore(path)

			cfg := oauth.FlowConfig{
				Endpoint: oauth2.Endpoint{
					AuthURL:  "https://auth.openai.com/oauth/authorize",
					TokenURL: "https://auth.openai.com/oauth/token",
				},
				ClientID:     "app_EMoamEEZ73f0CkXaXp7hrann",
				CallbackPort: 1455,
				Scopes:       []string{"openid", "profile", "email", "offline_access"},
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Opening browser to authenticate with OpenAI...")

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			token, err := oauth.RunAuthCodeFlow(ctx, cfg)
			if err != nil {
				return fmt.Errorf("run auth flow: %w", err)
			}

			if err := store.Save(token); err != nil {
				return fmt.Errorf("save token: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated. Token saved to %s.\n", path)
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "how long to wait for browser auth")
	cmd.AddCommand(newLoginCodexStatusCommand())
	return cmd
}

func newLoginCodexStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current Codex authentication status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := oauth.DefaultTokenPath()
			if err != nil {
				return fmt.Errorf("get token path: %w", err)
			}

			store := oauth.NewTokenStore(path)

			token, err := store.Load()
			if err != nil {
				if errors.Is(err, oauth.ErrNoToken) {
					fmt.Fprintln(cmd.OutOrStdout(), "Not authenticated. Run 'steiner login codex' to authenticate.")
					return nil
				}
				return fmt.Errorf("load token: %w", err)
			}

			if token.Expiry.IsZero() {
				fmt.Fprintln(cmd.OutOrStdout(), "Token expires: never")
				fmt.Fprintln(cmd.OutOrStdout(), "Status: valid")
				return nil
			}

			remaining := time.Until(token.Expiry)
			minutes := int(remaining.Minutes())
			fmt.Fprintf(cmd.OutOrStdout(), "Token expires: %s (in %d minutes)\n", token.Expiry.Format(time.RFC3339), minutes)

			if remaining < 5*time.Minute {
				fmt.Fprintln(cmd.OutOrStdout(), "Status: needs refresh")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Status: valid")
			}

			return nil
		},
	}
}
