package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func TestWorkflowHandoffModalHeaderText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		next     string
		wantText string
	}{
		{
			name:     "implement workflow",
			next:     "implement",
			wantText: "Continue to implementation?",
		},
		{
			name:     "review workflow",
			next:     "review",
			wantText: "Continue to review?",
		},
		{
			name:     "default workflow",
			next:     "custom",
			wantText: "Continue to the next workflow?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			payload := output.WorkflowHandoffEvent{
				Next:    tt.next,
				Target:  "test-target",
				Message: "",
			}
			selection := interactive.WorkflowHandoffModelSelection{
				ModelAlias:  "gpt-4",
				SourceLabel: "openai",
			}
			m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, selection)

			rendered := stripANSI(m.renderWorkflowHandoffModal())

			if !strings.Contains(rendered, tt.wantText) {
				t.Errorf("modal header = %q, want to contain %q", rendered, tt.wantText)
			}
		})
	}
}

func TestWorkflowHandoffModalWarningText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		next     string
		wantText string
	}{
		{
			name:     "implement job name",
			next:     "implement",
			wantText: "This will clear the context and start the 'implement' job",
		},
		{
			name:     "review job name",
			next:     "review",
			wantText: "This will clear the context and start the 'review' job",
		},
		{
			name:     "custom job name",
			next:     "custom-workflow",
			wantText: "This will clear the context and start the 'custom-workflow' job",
		},
		{
			name:     "empty job name defaults to workflow",
			next:     "",
			wantText: "This will clear the context and start the 'workflow' job",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			payload := output.WorkflowHandoffEvent{
				Next:    tt.next,
				Target:  "test-target",
				Message: "",
			}
			selection := interactive.WorkflowHandoffModelSelection{
				ModelAlias:  "gpt-4",
				SourceLabel: "openai",
			}
			m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, selection)

			rendered := stripANSI(m.renderWorkflowHandoffModal())

			if !strings.Contains(rendered, tt.wantText) {
				t.Errorf("warning text = %q, want to contain %q", rendered, tt.wantText)
			}
		})
	}
}

func TestWorkflowHandoffModalBoldLabels(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	payload := output.WorkflowHandoffEvent{
		Next:    "implement",
		Target:  "test-target",
		Message: "",
	}
	selection := interactive.WorkflowHandoffModelSelection{
		ModelAlias:  "gpt-4",
		SourceLabel: "openai",
	}
	m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, selection)

	// Render the modal with ANSI codes intact
	rendered := m.renderWorkflowHandoffModal()

	// Check for bold formatting in the output (ANSI bold is \x1b[1m)
	if !strings.Contains(rendered, "Model:") {
		t.Errorf("modal should contain 'Model:' label")
	}
	if !strings.Contains(rendered, "Planning folder:") {
		t.Errorf("modal should contain 'Planning folder:' label")
	}

	// Also check that the stripped version contains these labels
	stripped := stripANSI(rendered)
	if !strings.Contains(stripped, "Model:") {
		t.Errorf("stripped modal should contain 'Model:' label")
	}
	if !strings.Contains(stripped, "Planning folder:") {
		t.Errorf("stripped modal should contain 'Planning folder:' label")
	}
}

func TestWorkflowHandoffModalModelLineRendering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		modelAlias   string
		sourceLabel  string
		wantContains string
	}{
		{
			name:         "with model alias and source",
			modelAlias:   "gpt-4",
			sourceLabel:  "openai",
			wantContains: "gpt-4",
		},
		{
			name:         "with model alias only",
			modelAlias:   "claude-3",
			sourceLabel:  "",
			wantContains: "claude-3",
		},
		{
			name:         "empty model alias",
			modelAlias:   "",
			sourceLabel:  "",
			wantContains: "Model:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			payload := output.WorkflowHandoffEvent{
				Next:    "implement",
				Target:  "test-target",
				Message: "",
			}
			selection := interactive.WorkflowHandoffModelSelection{
				ModelAlias:  tt.modelAlias,
				SourceLabel: tt.sourceLabel,
			}
			m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, selection)

			rendered := stripANSI(m.renderWorkflowHandoffModal())

			if !strings.Contains(rendered, tt.wantContains) {
				t.Errorf("model line = %q, want to contain %q", rendered, tt.wantContains)
			}
		})
	}
}

func TestWorkflowHandoffModalPlanningFolder(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	payload := output.WorkflowHandoffEvent{
		Next:    "implement",
		Target:  ".project_planning/2026-06-16_my-feature",
		Message: "",
	}
	selection := interactive.WorkflowHandoffModelSelection{
		ModelAlias:  "gpt-4",
		SourceLabel: "openai",
	}
	m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, selection)

	rendered := stripANSI(m.renderWorkflowHandoffModal())

	if !strings.Contains(rendered, "Planning folder:") {
		t.Errorf("modal should contain 'Planning folder:' label")
	}
	if !strings.Contains(rendered, ".project_planning/2026-06-16_my-feature") {
		t.Errorf("modal should contain the target path")
	}
}

func TestWorkflowHandoffModalWithAttachedPickerKeepsLineWidthsBounded(t *testing.T) {
	useTrueColor(t)
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	payload := output.WorkflowHandoffEvent{
		Next:    "implement",
		Target:  ".project_planning/2026-06-23_charm-tui-v2",
		Message: "handoff now",
	}
	selection := interactive.WorkflowHandoffModelSelection{
		ModelAlias:  "deepseek-v4-flash",
		SourceLabel: "default",
	}
	m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, selection)
	m.modelPicker = m.modelPicker.OpenForWorkflowHandoff(
		"Select model for implementation",
		[]string{"deepseek-v4-flash", "deepseek-v4-pro-think-max", "deepseek-v4-pro-think-std"},
		"deepseek-v4-flash",
	)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height

	attached := m.modelPicker.ViewAttached(m.workflowHandoff.InnerWidth())
	for _, line := range strings.Split(attached, "\n") {
		if lipgloss.Width(line) > m.workflowHandoff.InnerWidth() {
			t.Fatalf("attached picker line width = %d, want <= %d for line %q", lipgloss.Width(line), m.workflowHandoff.InnerWidth(), stripANSI(line))
		}
	}

	rendered := m.renderWorkflowHandoffModal()
	wantWidth := m.workflowHandoff.overlayWidth()
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) != wantWidth {
			t.Fatalf("line width = %d, want %d for line %q", lipgloss.Width(line), wantWidth, stripANSI(line))
		}
	}
	if !strings.Contains(stripANSI(rendered), "deepseek-v4-pro-think-max") {
		t.Fatalf("rendered modal = %q, want attached picker content", stripANSI(rendered))
	}
}
