package core

// What an AI may do only with a person's yes
// (docs/security/2026-11-26-security-review.md, item 2). Anything the AI
// reads (a web page, a Confluence page, a file, a tool's output) can carry
// instructions; an AI steered that way should not be able to run commands
// or change the project's flows and tools on its own. So these built-ins,
// called by an agent through the bridge, are asked in the app first (with
// "Allow for this run", as Claude's and Copilot's own prompts). Flow steps
// that use them directly were approved with the flow and are not asked;
// a person trying a built-in on the Tools screen is not asked either.

import (
	"context"
	"fmt"

	"codegen/internal/extension"
)

// askBefore: the built-ins an agent needs a person's yes for, and what
// they would do.
var askBefore = map[string]string{
	"run_command":    "run a shell command",
	SaveFlowTool:     "save a flow (a new version)",
	RunFlowTool:      "run a flow",
	ScheduleFlowTool: "schedule a flow",
	SaveToolTool:     "save a tool",
}

type byPersonKey struct{}

// byPerson marks a context as a person's own action (not an agent's).
func byPerson(ctx context.Context) context.Context {
	return context.WithValue(ctx, byPersonKey{}, true)
}

// agentActionAllowed asks a person before an agent's built-in call runs a
// command or changes flows and tools.
func (h *Hub) agentActionAllowed(ctx context.Context, name string, args map[string]any) error {
	what, ok := askBefore[name]
	if !ok || ctx.Value(byPersonKey{}) != nil {
		return nil
	}
	agent := "agents"
	if a := h.activeRun(); a != nil {
		agent = a.Session.ID
	} else if fr := h.onlyFlowRun(); fr != "" {
		agent = "flow:" + fr
	}
	input := map[string]any{"description": "The AI wants to " + what}
	for k, v := range args {
		if k == "flow" { // a whole flow's JSON: too long to show; its name says enough
			if m, ok := v.(map[string]any); ok {
				input["flow"] = m["name"]
			}
			continue
		}
		input[k] = v
	}
	ans := h.AskPermission(ctx, extension.PermissionRequest{Agent: agent, Tool: name, Input: input})
	if !ans.Allow {
		return fmt.Errorf("not allowed: %s", firstNonEmpty(ans.Message, "the person did not let the AI "+what))
	}
	return nil
}
