package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/metadata"
)

var metadataCacheFactory = runtimeMetadataCache

func newModelMetadataCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model-metadata",
		Short: "Manage model metadata cache",
	}
	cmd.AddCommand(newModelMetadataStatusCommand())
	cmd.AddCommand(newModelMetadataRefreshCommand())
	cmd.AddCommand(newModelMetadataClearCommand())
	return cmd
}

func newModelMetadataStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show model metadata cache status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cache := metadataCacheFactory(nil)
			return printMetadataStatus(cmd.OutOrStdout(), cache)
		},
	}
}

func newModelMetadataRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force refresh of model metadata cache",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cache := metadataCacheFactory(nil)
			if err := cache.Refresh(cmd.Context()); err != nil {
				return fmt.Errorf("refresh cache: %w", err)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "model metadata cache refreshed"); err != nil {
				return fmt.Errorf("write refresh status: %w", err)
			}
			return nil
		},
	}
}

func newModelMetadataClearCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove model metadata cache files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cache := metadataCacheFactory(nil)
			if err := cache.Clear(); err != nil {
				return fmt.Errorf("clear cache: %w", err)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "model metadata cache cleared"); err != nil {
				return fmt.Errorf("write clear status: %w", err)
			}
			return nil
		},
	}
}

func printMetadataStatus(out io.Writer, cache *metadata.Cache) error {
	path := cache.CachePath()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return writeMetadataStatus(out, path, 0, "missing", "missing", 0)
		}
		return fmt.Errorf("stat cache: %w", err)
	}

	meta, err := cache.LoadMetadata()
	if err != nil {
		return fmt.Errorf("load cache metadata: %w", err)
	}
	age := "unknown"
	if !meta.DownloadedAt.IsZero() {
		age = time.Since(meta.DownloadedAt).Round(time.Minute).String()
	}
	freshness := "expired"
	if meta.ExpiresAt.IsZero() {
		freshness = "unknown"
	} else if cache.IsFresh() {
		freshness = "fresh"
	}

	modelCount := 0
	if data, err := cache.Load(); err == nil && data != nil {
		modelCount = metadata.CountModels(data)
	}

	return writeMetadataStatus(out, path, info.Size(), age, freshness, modelCount)
}

func writeMetadataStatus(out io.Writer, path string, sizeBytes int64, age string, freshness string, modelCount int) error {
	if _, err := fmt.Fprintf(out, "cache_path: %s\nsize_bytes: %d\nage: %s\nfreshness: %s\nmodel_count: %d\n", path, sizeBytes, age, freshness, modelCount); err != nil {
		return fmt.Errorf("write cache status: %w", err)
	}
	return nil
}

func runtimeMetadataCache(httpClient *http.Client) *metadata.Cache {
	return &metadata.Cache{
		Dir:        metadata.DefaultCacheDir(),
		HTTPClient: httpClient,
	}
}
