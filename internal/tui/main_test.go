package tui

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "steiner-tui-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for tui tests: %v\n", err)
		os.Exit(1)
	}
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	code := m.Run()
	os.Setenv("HOME", oldHome)
	os.RemoveAll(tmp)
	os.Exit(code)
}
