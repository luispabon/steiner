// Command gen-third-party-notices writes the third-party license notice
// inventory for Steiner release binaries.
//
// Run it from the repository root. Without -check it writes the notices file;
// with -check it verifies the committed file is up to date.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/fsatomic"
	"github.com/luispabon/steiner/internal/licensescan"
)

func main() {
	if err := run(); err != nil {
		var stale *staleError
		if errors.As(err, &stale) {
			fmt.Fprintln(os.Stderr, stale.Error())
		} else {
			fmt.Fprintf(os.Stderr, "gen-third-party-notices: %v\n", err)
		}
		os.Exit(1)
	}
}

// staleError reports that the notices file on disk does not match the scan.
type staleError struct {
	path string
}

func (e *staleError) Error() string {
	return fmt.Sprintf("%s is out of date: run 'make notices' and commit the result", e.path)
}

// checkFile reports whether path already holds data. A missing file and any
// differing content are both reported as a staleError.
func checkFile(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &staleError{path: path}
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Equal(existing, data) {
		return &staleError{path: path}
	}
	return nil
}

func run() error {
	out := flag.String("out", "THIRD_PARTY_NOTICES.md", "path of the notices file to write or check")
	check := flag.Bool("check", false, "verify the notices file is up to date instead of writing it")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "usage: gen-third-party-notices [-out file] [-check]\n\nRun from the repository root.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	report, err := licensescan.Scan(context.Background(), ".", nil)
	if err != nil {
		return fmt.Errorf("scan licenses: %w", err)
	}
	data := licensescan.Render(report)

	if *check {
		return checkFile(*out, data)
	}

	if err := fsatomic.WriteFile(*out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	return nil
}
