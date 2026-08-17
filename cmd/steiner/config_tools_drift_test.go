package main

import (
	"slices"
	"sort"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestReservedToolNamesMatchesBuiltins(t *testing.T) {
	builtinNames := make([]string, 0)
	for _, def := range builtin.Builtins(builtin.Env{}) {
		builtinNames = append(builtinNames, def.Name)
	}
	sort.Strings(builtinNames)

	reserved := config.ReservedToolNames()

	if !slices.Equal(reserved, builtinNames) {
		t.Errorf("ReservedToolNames() = %v, want %v (built-in names diverged)", reserved, builtinNames)
	}
}
