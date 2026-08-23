package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/modelcatalog"
)

var modelCatalogServiceFactory = buildModelCatalogService
var modelCatalogCacheFactory = modelcatalog.NewCache

func newModelsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage discovered provider models",
	}
	cmd.AddCommand(newModelsRefreshCommand())
	cmd.AddCommand(newModelsStatusCommand())
	return cmd
}

func newModelsRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force refresh of discovered provider models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, endpoints, err := buildModelsRuntime(cmd)
			if err != nil {
				return err
			}
			if !service.DiscoveryEnabled {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "model discovery disabled")
				return err
			}
			if len(endpoints) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "no model providers configured")
				return err
			}

			report := service.RefreshAll(cmd.Context(), endpoints, modelcatalog.RefreshOptions{Force: true})
			failed := 0
			for _, result := range report.Results {
				if result.Err != nil || result.Status == modelcatalog.RefreshStatusFailed {
					failed++
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: failed: %v\n", result.Alias, result.Err); err != nil {
						return err
					}
					continue
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: ok\n", result.Alias); err != nil {
					return err
				}
			}
			if failed == len(report.Results) {
				return fmt.Errorf("model refresh failed for all providers")
			}
			return nil
		},
	}
}

func newModelsStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show discovered provider model cache status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, endpoints, err := buildModelsRuntime(cmd)
			if err != nil {
				return err
			}
			if !service.DiscoveryEnabled {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "model discovery: disabled")
				return err
			}
			if len(endpoints) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "no model providers configured")
				return err
			}

			cache := modelCatalogCacheFactory("")
			for _, endpoint := range endpoints {
				_, found, err := cache.Load(endpoint.Alias, endpoint.Type, endpoint.BaseURL)
				if err != nil {
					return fmt.Errorf("load cache for %s: %w", endpoint.Alias, err)
				}
				freshness := "missing"
				if found {
					freshness = "stale"
					if cache.IsFresh(endpoint.Alias, endpoint.Type, endpoint.BaseURL) {
						freshness = "fresh"
					}
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", endpoint.Alias, freshness); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func buildModelsRuntime(cmd *cobra.Command) (*modelcatalog.Service, []modelcatalog.Endpoint, error) {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, nil, fmt.Errorf("read config path: %w", err)
	}
	cfg, err := config.Load(config.LoadOptions{CLI: config.CLIOverrides{ConfigPath: configPath}})
	if err != nil {
		return nil, nil, err
	}
	service, endpoints, _ := modelCatalogServiceFactory(&cfg, runtimeHTTPClient())
	return service, endpoints, nil
}
