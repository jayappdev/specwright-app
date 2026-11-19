package core

// Built-in tools that send data out of the computer (builtin.SendsOut):
// http_request with a method that changes things (POST, PUT, …) and notify
// to Slack, Teams or a webhook. Reading and desktop notifications need
// nothing; these need a person:
//
//   - in a flow, the step (or an output's "call a tool" action) is
//     approved per project, like a "Run a command" step, and listed with
//     the commands in Run flows ("Commands and messages this flow sends").
//     A step whose arguments the AI works out is approved as such. A run
//     that would send without an approval stops.
//   - for an agent, each call is asked in the app (as Copilot's and
//     Claude's own permission prompts are).

import (
	"context"
	"fmt"
	"strings"

	"codegen/internal/builtin"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

// outwardTool says a built-in can send data out (some calls of it).
var outwardTool = map[string]bool{"http_request": true, "notify": true}

func strArgs(m map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// outwardEntries are a flow's steps and actions that send data out, as
// approval entries (Step is unique: an action is "<step>/action<n>").
func outwardEntries(f *flow.Flow) []FlowCommand {
	var out []FlowCommand
	add := func(step, title, tool string, args map[string]string, aiArgs string) {
		bare := strings.TrimPrefix(tool, BuiltinPrefix)
		if !IsBuiltinTool(tool) || !outwardTool[bare] {
			return
		}
		sends, desc := builtin.SendsOut(bare, strArgs(args))
		if strings.TrimSpace(aiArgs) != "" {
			sends, desc = true, "arguments the AI works out: "+clipText(aiArgs, 160)
		}
		if sends {
			out = append(out, FlowCommand{Step: step, Title: title, Command: bare + ": " + desc, Kind: "sends"})
		}
	}
	for _, n := range f.Nodes {
		title := firstNonEmpty(n.Title, n.ID)
		if n.Type == "tool" {
			add(n.ID, title, n.Tool, n.Args, n.Instruction)
		}
		for i, a := range n.Actions {
			if a.Type == "tool" {
				add(fmt.Sprintf("%s/action%d", n.ID, i+1), title+" › "+strings.TrimPrefix(a.Tool, BuiltinPrefix), a.Tool, a.Args, "")
			}
		}
	}
	return out
}

// outwardAllowed refuses a flow's built-in call that sends data out
// without the step's approval (checked again at run time: the AI may have
// chosen the arguments, or a template filled them in).
func (h *Hub) outwardAllowed(root string, fr *flowRun, step, name string, args map[string]any) error {
	bare := strings.TrimPrefix(name, BuiltinPrefix)
	if !outwardTool[bare] {
		return nil
	}
	sends, desc := builtin.SendsOut(bare, args)
	if !sends || fr.flow == nil {
		return nil
	}
	for _, c := range h.flowCommands(root, fr.flow) {
		if c.Kind == "sends" && (c.Step == step || strings.HasPrefix(c.Step, step+"/action")) && c.Approved {
			return nil
		}
	}
	return fmt.Errorf("%s would %s, which is not approved for this project: approve the step in Run flows (Commands and messages this flow sends)", bare, desc)
}

// agentOutwardAllowed asks a person before an agent's built-in call sends
// data out.
func (h *Hub) agentOutwardAllowed(ctx context.Context, name string, args map[string]any) error {
	sends, desc := builtin.SendsOut(name, args)
	if !sends {
		return nil
	}
	agent := "agents"
	if a := h.activeRun(); a != nil {
		agent = a.Session.ID
	}
	input := map[string]any{"description": "SpecWright's " + name + " would " + desc}
	for k, v := range args {
		if k != "json" && k != "body" && k != "message" {
			input[k] = v
		}
	}
	ans := h.AskPermission(ctx, extension.PermissionRequest{Agent: agent, Tool: name, Input: input})
	if !ans.Allow {
		return fmt.Errorf("not allowed: %s", firstNonEmpty(ans.Message, "the person did not approve "+desc))
	}
	return nil
}
