package main

import (
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

type interactiveSkills struct {
	mu      sync.RWMutex
	enabled map[string]bool
	order   []string
}

type requestSnapshotStore struct {
	mu       sync.RWMutex
	snapshot *output.RequestContextSnapshot
}

func newInteractiveSkills(skillNames []string) *interactiveSkills {
	enabled := make(map[string]bool, len(skillNames))
	for _, name := range skillNames {
		enabled[name] = true
	}
	return &interactiveSkills{
		enabled: enabled,
		order:   append([]string(nil), skillNames...),
	}
}

func (s *interactiveSkills) Set(name string, enabled bool) {
	if s == nil || strings.TrimSpace(name) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled == nil {
		s.enabled = make(map[string]bool)
	}
	s.enabled[name] = enabled
}

func (s *interactiveSkills) Snapshot() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.order))
	for _, name := range s.order {
		if s.enabled[name] {
			names = append(names, name)
		}
	}
	return names
}

func (s *requestSnapshotStore) Store(snapshot output.RequestContextSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := output.RequestContextSnapshot{
		Model:       snapshot.Model,
		Messages:    append([]provider.Message(nil), snapshot.Messages...),
		Tools:       append([]provider.ToolSpec(nil), snapshot.Tools...),
		MaxTokens:   cloneOptionalInt(snapshot.MaxTokens),
		Blocks:      append([]prompt.ContextBlock(nil), snapshot.Blocks...),
		ModelBudget: snapshot.ModelBudget,
	}
	s.snapshot = &cloned
}

func (s *requestSnapshotStore) Snapshot() (output.RequestContextSnapshot, bool) {
	if s == nil {
		return output.RequestContextSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil {
		return output.RequestContextSnapshot{}, false
	}
	cloned := output.RequestContextSnapshot{
		Model:       s.snapshot.Model,
		Messages:    append([]provider.Message(nil), s.snapshot.Messages...),
		Tools:       append([]provider.ToolSpec(nil), s.snapshot.Tools...),
		MaxTokens:   cloneOptionalInt(s.snapshot.MaxTokens),
		Blocks:      append([]prompt.ContextBlock(nil), s.snapshot.Blocks...),
		ModelBudget: s.snapshot.ModelBudget,
	}
	return cloned, true
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
