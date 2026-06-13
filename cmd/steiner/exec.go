package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func runExecMode(cmd *cobra.Command, flags *cliFlags, args []string) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	defer closeRuntime(&rt)

	promptText := strings.TrimSpace(strings.Join(args, " "))
	if promptText == "" {
		promptText, err = readPromptFromInput(rt.sharedInput)
		if err != nil {
			return err
		}
	}
	if promptText == "" {
		return fmt.Errorf("exec mode requires a prompt")
	}
	rt.events.Emit(output.NewUserInputEvent(promptText, "exec"))

	execMaxTurns := flags.maxTurns
	if execMaxTurns <= 0 {
		execMaxTurns = rt.cfg.Limits.MaxTurns
	}
	if !flags.enableStreaming {
		rt.human.Printf("Waiting for response…\n")
	}
	_, err = cliRunner{
		runtime: rt,
		approver: agent.NewEventingApprover(
			rt.events,
			stdinApprovalResponder{reader: approvalReader(rt)},
		),
		maxTurns:           execMaxTurns,
		runMode:            "exec",
		streamingPreferred: flags.enableStreaming,
		cavemanMode:        func() bool { return rt.cfg.CavemanMode },
		humanizerMode:      func() bool { return rt.cfg.HumanizerMode },
	}.Run(cmd.Context(), []agent.Message{{Role: agent.MessageRoleUser, Content: promptText}}, nil, nil)
	if err != nil {
		return err
	}
	return nil
}

func readPromptFromInput(reader *bufio.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("input is required")
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func lastAssistantReply(messages []agent.Message) string {
	if msg, ok := agent.LastAssistantMessage(messages); ok {
		return strings.TrimSpace(msg.Content)
	}
	return ""
}
