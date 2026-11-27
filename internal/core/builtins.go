package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		Secret: h.secretByName, // http_request, notify (desktop.go)
		Notify: h.desktopNotify,
		Ask:    h.askQuestions, // ask_user (questions.go)
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

// policyKeepEnv is the machine policy's keepEnvVars (safeenv.SetKeep).
func policyKeepEnv() []string {
	var pf policyFile
	if b, err := os.ReadFile(PolicyPath); err == nil && json.Unmarshal(b, &pf) == nil {
		return pf.KeepEnvVars
	}
	return nil
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
	for _, t := range h.appTools() {
		out = append(out, BuiltinTool{BuiltinInfo: t, Enabled: !off[t.Name]})
	}
	return out
}

// appTools are SpecWright's own agent tools: how agents change, schedule
// and run flows, read the run history and propose tools. They are offered
// as their own MCP tools and through extension_managed.
func (h *Hub) appTools() []extension.BuiltinInfo {
	out := []extension.BuiltinInfo{
		saveFlowInfo(),     // flowsave_agent.go
		scheduleFlowInfo(), // flowschedule_agent.go
		runFlowInfo(),      // runflow_agent.go
		saveToolInfo(),     // toolsave_agent.go
	}
	if h.Store.HasDB() {
		out = append(out, historySearchInfo()) // history_search.go
	}
	for i := range out {
		out[i].App = true
	}
	return out
}

// Builtins implements extension.Provider: the enabled tools for agents.
func (h *Hub) Builtins() []extension.BuiltinInfo {
	var out []extension.BuiltinInfo
	for _, t := range h.BuiltinTools() { // the built-ins and SpecWright's own (appTools)
		if t.Enabled {
			out = append(out, t.BuiltinInfo)
		}
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
	if e := h.agentOutwardAllowed(ctx, name, args); e != nil { // outward.go: a person approves what sends data out
		return "", e
	}
	if e := h.agentActionAllowed(ctx, name, args); e != nil { // agentactions.go: a person approves commands and changes
		return "", e
	}
	if e := h.agentNetworkAllowed(ctx, name, args); e != nil { // agentactions.go: a person approves reaching inside addresses
		return "", e
	}
	switch name {
	case SaveFlowTool:
		out, err = h.agentSaveFlow(args)
	case ScheduleFlowTool:
		out, err = h.agentScheduleFlow(args)
	case HistorySearchTool:
		out, err = h.agentHistorySearch(args)
	case RunFlowTool:
		out, err = h.agentRunFlow(ctx, args)
	case SaveToolTool:
		out, err = h.agentSaveTool(args)
	default:
		out, err = h.tools.Call(ctx, name, args)
	}
	out = h.Store.Redactor().String(out)
	ev := bus.Event{Type: "builtin.result", Summary: name + map[bool]string{true: " ok", false: " failed"}[err == nil],
		Data: map[string]any{"name": name, "args": args, "ok": err == nil, "duration_ms": time.Since(start).Milliseconds(), "output": trunc(firstNonEmpty(out, errText(err)), 2000)}}
	if a := h.activeRun(); a != nil {
		h.emitRun(a, ev)
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
	ctx, cancel := context.WithTimeout(byPerson(h.ctx), 10*time.Minute)
	defer cancel()
	return h.RunBuiltin(ctx, name, args)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

// AgentTool is a tool an agent is offered, as the Prompt Playground lists it.
type AgentTool struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Group    string `json:"group"`
	Desc     string `json:"desc"`
	App      bool   `json:"app,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// AgentTools are the built-in and SpecWright tools agents get here (the
// project's own tools are listed with the tools), for the Playground.
func (h *Hub) AgentTools() []AgentTool {
	out := []AgentTool{}
	for _, b := range h.Builtins() {
		d := b.Description
		if i := strings.IndexAny(d, "\n"); i >= 0 {
			d = d[:i]
		}
		out = append(out, AgentTool{Name: b.Name, Title: b.Title, Group: b.Group, Desc: clipText(d, 260), App: b.App, ReadOnly: b.ReadOnly})
	}
	return out
}

// ChartPNG saves a PNG of a chart the chart tool drew (its SVG, which holds
// the chart's data) in the project, and returns where (workspace path).
// path "" saves it next to the SVG's name in out/charts.
func (h *Hub) ChartPNG(svg, path string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	spec, err := builtin.ChartFromSVG(svg)
	if err != nil {
		return "", err
	}
	data, err := builtin.ChartPNG(spec)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		name := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(firstNonEmpty(spec.Title, "chart")), "-"), "-")
		path = "out/charts/" + firstNonEmpty(name, "chart") + "-" + time.Now().Format("20060102-150405") + ".png"
	}
	if !strings.HasSuffix(strings.ToLower(path), ".png") {
		path += ".png"
	}
	abs := filepath.Join(root, filepath.FromSlash(path))
	if rel, err := filepath.Rel(root, abs); err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("save the PNG inside the project")
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(root, abs)
	return filepath.ToSlash(rel), nil
}
