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
			fmt.Fprintln(cmd.OutOrStdout(), "model metadata cache refreshed")
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
			fmt.Fprintln(cmd.OutOrStdout(), "model metadata cache cleared")
			return nil
		},
	}
}

func printMetadataStatus(out io.Writer, cache *metadata.Cache) error {
	path := cache.CachePath()
	fmt.Fprintf(out, "cache_path: %s\n", path)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "age: missing\n")
			fmt.Fprintf(out, "size_bytes: 0\n")
			fmt.Fprintf(out, "freshness: missing\n")
			fmt.Fprintf(out, "model_count: 0\n")
			return nil
		}
		return fmt.Errorf("stat cache: %w", err)
	}

	fmt.Fprintf(out, "size_bytes: %d\n", info.Size())

	meta, err := cache.LoadMetadata()
	if err != nil {
		return fmt.Errorf("load cache metadata: %w", err)
	}
	age := "unknown"
	if !meta.DownloadedAt.IsZero() {
		age = time.Since(meta.DownloadedAt).Round(time.Minute).String()
	}
	fmt.Fprintf(out, "age: %s\n", age)

	freshness := "unknown"
	switch {
	case meta.ExpiresAt.IsZero():
		freshness = "unknown"
	case cache.IsFresh():
		freshness = "fresh"
	default:
		freshness = "expired"
	}
	fmt.Fprintf(out, "freshness: %s\n", freshness)

	if data, err := cache.Load(); err == nil && data != nil {
		fmt.Fprintf(out, "model_count: %d\n", metadata.CountModels(data))
		return nil
	}
	fmt.Fprintf(out, "model_count: 0\n")
	return nil
}

func runtimeMetadataCache(httpClient *http.Client) *metadata.Cache {
	return &metadata.Cache{
		Dir:        metadata.DefaultCacheDir(),
		HTTPClient: httpClient,
	}
}
