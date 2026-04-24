package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <markdown-file>\n", os.Args[0])
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	input := string(data)

	width := 76

	// Use EXACTLY what steiner uses
	r, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(width-4),
		glamour.WithPreservedNewLines(),
		glamour.WithStandardStyle("dark"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating renderer: %v\n", err)
		os.Exit(1)
	}

	out, err := r.Render(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error rendering: %v\n", err)
		os.Exit(1)
	}

	// EXACTLY what steiner does - normalize trailing newlines
	out = strings.TrimRight(out, "\n") + "\n"

	os.Stdout.Write([]byte(out))
}