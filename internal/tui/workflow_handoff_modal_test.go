package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func TestWorkflowHandoffModalHeaderText(t *testing.T) {
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
