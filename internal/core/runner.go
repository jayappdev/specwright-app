package core

// Runner windows: a flow run in a SpecWright window of its own. Each is
// another SpecWright process started as `SpecWright runner --workspace DIR
// --flow ID` (Wails v2 has one window per app), with the same agent and
// model; a session token goes in its environment, not on its command line.
// It shows that flow's Run screen only and leaves schedules, triggers and
// housekeeping to this main window (Options.Runner).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"codegen/internal/flow"
)

// RunnerTokenEnv carries the session token to a runner window.
const RunnerTokenEnv = "SPECWRIGHT_RUNNER_TOKEN"

// runnerCommand is the command that opens a runner window for a flow.
func (h *Hub) runnerCommand(exe, flowID string) (*exec.Cmd, error) {
	h.mu.RLock()
	root, agentName, model, token := h.root, h.agentName, h.model, h.token
	h.mu.RUnlock()
	if root == "" {
		return nil, errors.New("open a workspace first")
	}
	if _, err := flow.Load(root, flowID); err != nil {
		return nil, fmt.Errorf("no flow %s: %v", flowID, err)
	}
	args := []string{"runner", "--workspace", root, "--flow", flowID, "--agent", agentName}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	if token != "" {
		cmd.Env = append(cmd.Env, RunnerTokenEnv+"="+token)
	}
	detach(cmd) // it lives on when this window closes
	return cmd, nil
}

// OpenRunnerWindow opens flowID in a SpecWright window of its own.
func (h *Hub) OpenRunnerWindow(flowID string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd, err := h.runnerCommand(exe, flowID)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open the runner window: %v", err)
	}
	go func() { _ = cmd.Wait() }() // reap it when it closes
	return nil
}

// RunnerFlow is the flow this window runs ("" in the main window).
func (h *Hub) RunnerFlow() string { return h.runnerFlow }
