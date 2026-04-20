package tool

import (
	"time"

	"github.com/luispabon/steiner/internal/config"
)

type ToolDef struct {
	Name            string
	ExecPath        string
	Subcommand      string
	Description     string
	ParameterSchema map[string]any
	Timeout         time.Duration
	Approval        config.ApprovalMode
}
