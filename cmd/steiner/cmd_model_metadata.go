package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/metadata"
)

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
			cache := runtimeMetadataCache(nil)
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
			cache := runtimeMetadataCache(nil)
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
			cache := runtimeMetadataCache(nil)
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
			fmt.Fprintf(out, "status: not_found\n")
			return nil
		}
		return fmt.Errorf("stat cache: %w", err)
	}

	fmt.Fprintf(out, "size_bytes: %d\n", info.Size())

	meta, _ := cache.LoadMetadata()
	if !meta.DownloadedAt.IsZero() {
		age := time.Since(meta.DownloadedAt)
		fmt.Fprintf(out, "age: %s\n", age.Round(time.Minute))
	}
	if !meta.ExpiresAt.IsZero() {
		fresh := cache.IsFresh()
		if fresh {
			fmt.Fprintf(out, "status: fresh\n")
		} else {
			fmt.Fprintf(out, "status: expired\n")
		}
	}

	if data, err := cache.Load(); err == nil && data != nil {
		// Count models — simple heuristic
		var root map[string]interface{}
		if jsonErr := json.Unmarshal(data, &root); jsonErr == nil {
			if models, ok := root["models"].(map[string]interface{}); ok {
				fmt.Fprintf(out, "model_count: %d\n", len(models))
			}
		}
	}
	return nil
}

func runtimeMetadataCache(httpClient *http.Client) *metadata.Cache {
	return &metadata.Cache{
		Dir:        metadataCacheDir(),
		HTTPClient: httpClient,
	}
}

func metadataCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "model-metadata")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "steiner", "model-metadata")
}
