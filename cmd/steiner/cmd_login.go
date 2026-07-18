package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/oauth"
)

var codexOAuthScopes = []string{"openid", "profile", "email", "offline_access", "api.connectors.read", "api.connectors.invoke"}

func writeStatusLine(w io.Writer, format string, args ...any) error {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}

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
	var debugURL bool

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
					AuthURL:  oauth.CodexAuthURL,
					TokenURL: oauth.CodexTokenURL,
				},
				ClientID:     oauth.CodexClientID,
				RedirectURI:  oauth.CodexRedirectURI,
				CallbackPort: 1455,
				CallbackPath: "/auth/callback",
				Scopes:       append([]string(nil), codexOAuthScopes...),
				ExtraParams: map[string]string{
					"id_token_add_organizations": "true",
					"codex_cli_simplified_flow":  "true",
					"originator":                 "codex",
				},
			}

			if debugURL {
				cfg.OnAuthURL = func(u string) {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Auth URL: %s\n", u)
				}
			}

			if err := writeStatusLine(cmd.OutOrStdout(), "Opening browser to authenticate with OpenAI...\n"); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			token, err := oauth.RunAuthCodeFlow(ctx, cfg)
			if err != nil {
				return fmt.Errorf("run auth flow: %w", err)
			}
			token = withOptionalCodexAPIKey(ctx, token)

			if err := store.Save(token); err != nil {
				return fmt.Errorf("save token: %w", err)
			}

			if err := writeStatusLine(cmd.OutOrStdout(), "Authenticated. Token saved to %s.\n", path); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "how long to wait for browser auth")
	cmd.Flags().BoolVar(&debugURL, "debug-url", false, "print the full authorization URL before opening the browser")
	cmd.AddCommand(newLoginCodexStatusCommand())
	return cmd
}

func withOptionalCodexAPIKey(ctx context.Context, token *oauth2.Token) *oauth2.Token {
	idToken, _ := token.Extra("id_token").(string)
	apiKey, err := oauth.ExchangeOpenAIAPIKey(ctx, oauth.CodexTokenURL, oauth.CodexClientID, idToken, nil)
	if err != nil {
		return token
	}
	return oauth.WithOpenAIAPIKey(token, apiKey)
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
					if err := writeStatusLine(cmd.OutOrStdout(), "Not authenticated. Run 'steiner login codex' to authenticate.\n"); err != nil {
						return err
					}
					return nil
				}
				return fmt.Errorf("load token: %w", err)
			}

			if token.Expiry.IsZero() {
				if err := writeStatusLine(cmd.OutOrStdout(), "Token expires: never\n"); err != nil {
					return err
				}
				if err := writeStatusLine(cmd.OutOrStdout(), "Status: valid\n"); err != nil {
					return err
				}
				return nil
			}

			remaining := time.Until(token.Expiry)
			minutes := int(remaining.Minutes())
			if err := writeStatusLine(cmd.OutOrStdout(), "Token expires: %s (in %d minutes)\n", token.Expiry.Format(time.RFC3339), minutes); err != nil {
				return err
			}

			if remaining < 5*time.Minute {
				if err := writeStatusLine(cmd.OutOrStdout(), "Status: needs refresh\n"); err != nil {
					return err
				}
			} else {
				if err := writeStatusLine(cmd.OutOrStdout(), "Status: valid\n"); err != nil {
					return err
				}
			}

			return nil
		},
	}
}
