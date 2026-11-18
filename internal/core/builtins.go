package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codegen/internal/builtin"
	"codegen/internal/bus"
	"codegen/internal/extension"
)

// Built-in tools are compiled into SpecWright: nobody can change them, and
// they are attached to every agent run (workflow steps and playground
// prompts) as their own MCP tools. IT can switch individual tools off in
// the machine policy with "disabledBuiltinTools": ["run_command", …].

// BuiltinTool is a built-in tool as the UI shows it.
type BuiltinTool struct {
	extension.BuiltinInfo
	Enabled bool `json:"enabled"`
}

func (h *Hub) newToolbox() *builtin.Box {
	return builtin.New(builtin.Config{
		// Same environment as "Compile project": the project's own package
		// caches, plus the proxy and registries when they are switched on
		// for agent runs. (h.Env is for project tools, which keep their
		// packages in .codegen/cache.)
		Env:   func() []string { return h.agentPackageEnv(h.Root()) },
		Trash: filepath.Join(h.Store.Dir(), "trash"),
		// during a Spec workflow step, commands run in the Spec Kit project
		// folder (spec-kit's scripts find their project from there)
		DefaultCwd: func() string {
			if a := h.activeRun(); a != nil && a.Session.Step != PlaygroundStep {
				return h.specRel()
			}
			return ""
		},
		Problems: func(ctx context.Context) (string, string, error) {
			res, err := h.Compile()
			if err != nil {
				return res.Command, err.Error(), err
			}
			if !res.OK {
				return res.Command, res.Output, fmt.Errorf("build failed")
			}
			return res.Command, res.Output, nil
		},
	})
}

func (h *Hub) disabledBuiltins() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(PolicyPath)
	if err != nil {
		return out
	}
	var pf policyFile
	if json.Unmarshal(b, &pf) == nil {
		for _, n := range pf.DisabledBuiltinTools {
			out[n] = true
		}
	}
	return out
}

// BuiltinTools lists every built-in tool with whether it is enabled here.
func (h *Hub) BuiltinTools() []BuiltinTool {
	off := h.disabledBuiltins()
	var out []BuiltinTool
	for _, t := range builtin.Tools() {
		out = append(out, BuiltinTool{BuiltinInfo: extension.BuiltinInfo{Name: t.Name, Title: t.Title, Description: t.Desc, InputSchema: t.Schema, ReadOnly: t.ReadOnly, Group: t.Group}, Enabled: !off[t.Name]})
	}
	return out
}

// Builtins implements extension.Provider: the enabled tools for agents.
func (h *Hub) Builtins() []extension.BuiltinInfo {
	var out []extension.BuiltinInfo
	for _, t := range h.BuiltinTools() {
		if t.Enabled {
			out = append(out, t.BuiltinInfo)
		}
	}
	if !h.disabledBuiltins()[SaveFlowTool] {
		out = append(out, saveFlowInfo()) // how agents change flows (flowsave_agent.go)
	}
	if !h.disabledBuiltins()[ScheduleFlowTool] {
		out = append(out, scheduleFlowInfo()) // how agents schedule flows (flowschedule_agent.go)
	}
	return out
}

// RunBuiltin implements extension.Provider: run a built-in tool in the open
// workspace and record it on the live run.
func (h *Hub) RunBuiltin(ctx context.Context, name string, args map[string]any) (string, error) {
	if h.disabledBuiltins()[name] {
		return "", fmt.Errorf("%s is turned off on this computer by policy", name)
	}
	start := time.Now()
	var out string
	var err error
	switch name {
	case SaveFlowTool:
		out, err = h.agentSaveFlow(args)
	case ScheduleFlowTool:
		out, err = h.agentScheduleFlow(args)
	default:
		out, err = h.tools.Call(ctx, name, args)
	}
	out = h.Store.Redactor().String(out)
	ev := bus.Event{Type: "builtin.result", Summary: name + map[bool]string{true: " ok", false: " failed"}[err == nil],
		Data: map[string]any{"name": name, "args": args, "ok": err == nil, "duration_ms": time.Since(start).Milliseconds(), "output": trunc(firstNonEmpty(out, errText(err)), 2000)}}
	if a := h.activeRun(); a != nil {
		ev.RunID, ev.Step = a.Session.ID, a.Session.Step
		_ = h.Store.Append(h.Bus.Publish(ev))
	} else {
		h.Bus.Publish(ev)
	}
	return out, err
}

// ErrBuiltinsAdminOnly is returned when someone outside admin mode tries to
// browse or run a built-in tool from the UI. Agents are not affected: they
// reach the built-ins over the agentbridge MCP server.
var ErrBuiltinsAdminOnly = errors.New("built-in tools can only be viewed and tried in admin mode; agents can still use them")

// BuiltinsVisible says whether the UI may list and try built-in tools: only
// in admin mode. Regular users never see them; agents always get them.
func (h *Hub) BuiltinsVisible() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.adminPriv != nil
}

// CallBuiltin lets an admin try a built-in tool directly (Tools screen).
func (h *Hub) CallBuiltin(name string, args map[string]any) (string, error) {
	if !h.BuiltinsVisible() {
		return "", ErrBuiltinsAdminOnly
	}
	if _, err := h.need(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Minute)
	defer cancel()
	return h.RunBuiltin(ctx, name, args)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
