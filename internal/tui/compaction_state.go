package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

type compactionState struct {
	active  bool
	summary string
}

func (c compactionState) Active() bool           { return c.active }
func (c compactionState) SuppressThinking() bool { return c.active }
func (c compactionState) SidebarLabel() string {
	if c.active {
		return "compacting…"
	}
	return c.summary
}

func newCompactionState(payload output.ContextCompactionEvent) compactionState {
	return compactionState{
		active:  payload.Severity == "compacting",
		summary: "",
	}
}

// setCompaction fans the compaction state out to every reader (model,
// content buffer, sidebar) from one place so the copies cannot desync.
func (m *Model) setCompaction(cs compactionState) {
	m.compaction = cs
	m.content.compaction = cs
	m.sidebar.compaction = cs
}

func appendStatusContext(base, fragment string) string {
	base = strings.TrimSpace(base)
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return base
	}
	if base == "" {
		return fragment
	}
	return base + " | " + fragment
}

func compactionStatusFragment(payload output.ContextCompactionEvent) string {
	parts := make([]string, 0, 3)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("compaction #%d", payload.CompactionCount))
	}
	if mode := strings.TrimSpace(payload.Mode); mode != "" {
		parts = append(parts, "mode "+mode)
	}
	if payload.Mode != "" || payload.SummaryTokenBudget > 0 || payload.ThresholdAchieved {
		parts = append(parts, fmt.Sprintf("threshold achieved %t", payload.ThresholdAchieved))
	}
	if payload.Severity == "critical" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart now")
	} else if payload.Severity == "warning" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart soon")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func sessionHealthStatusFragment(payload output.ContextSessionHealthEvent) string {
	parts := make([]string, 0, 3)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("compaction #%d", payload.CompactionCount))
	}
	if payload.Severity == "critical" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart now")
	} else if payload.Severity == "warning" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart soon")
	}
	return strings.Join(parts, " ")
}
