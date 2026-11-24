// Package agent drives AI coding CLIs (Claude Code, GitHub Copilot CLI) as
// child processes and normalizes what they print into Events. The app never
// stores CLI credentials: children inherit the user's environment and the
// CLIs' own logins, plus at most a token held in memory for this session.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Event is a normalized agent event. Types:
//
//	model.request  input_tokens, cached_tokens, model
//	model.response output_tokens, stop_reason, text?
//	model.delta    text (streamed partial text, copilot)
//	tool.call      tool, args, id
//	tool.result    tool, ok, id, output, duration_ms
//	run.usage      cost_usd, premium_requests, input_tokens, output_tokens
//	agent.log      line (stderr / unparsed output)
type Event struct {
	Type    string
	Summary string
	Data    map[string]any
}

// Request is one agent invocation.
type Request struct {
	Purpose string // step | generate | test
	Step    string // workflow step id, e.g. "specify"
	Prompt  string
	Dir     string // working directory (the workspace, or the Spec Kit project folder in it)
	// AddDirs are more folders the agent may read and write (the workspace
	// when Dir is a project folder inside it).
	AddDirs []string
	Model   string   // optional model override
	Env     []string // extra KEY=VALUE pairs (session token, bridge address)
	// MCPConfig is a path to an MCP config JSON naming the agentbridge server.
	MCPConfig  string
	AllowTools []string
	// Interactive: a person is watching (the Playground) and answers
	// permission questions in the app. Copilot then lets the hook decide
	// about paths outside the project too, instead of refusing them, and
	// asks about edits outside it (see Copilot.argsWith).
	Interactive bool
	// SessionID names the AI conversation (a UUID the caller makes). With
	// Resume it is an earlier conversation to continue: the AI still has
	// everything said in it (gathered context, answers), so the prompt is
	// only the new question. Without Resume it names the new conversation,
	// so it can be continued later.
	SessionID string
	Resume    bool
	// ReadOnly: the agent may read and answer, not edit files or run
	// commands (a Chatflow's chat: what it may do runs as the Chatflow's
	// own steps).
	ReadOnly bool
	// AskPermission asks the person in the app whether the agent may use a
	// tool (Copilot hosted through its SDK, in an interactive run); agentID
	// scopes "allow for this run". AutoAllow says which calls need no
	// question (SpecWright's own tools, edits inside the run's folders).
	AskPermission func(ctx context.Context, agentID, tool string, input map[string]any) (allow bool, message string)
	AutoAllow     func(tool string, input map[string]any) bool
	// AskUser puts the agent's question to the person (choices, or their
	// own words when freeform); it returns the answer and whether it was
	// typed rather than chosen.
	AskUser func(ctx context.Context, question string, choices []string, freeform bool) (answer string, typed bool, err error)
	// Hints are used only by the mock driver to decide which extension to call.
	Hints []ToolHint
}

// ToolHint names an extension a test expects the agent to call.
type ToolHint struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// Result is the outcome of a run.
type Result struct {
	Text            string
	Model           string
	CostUSD         float64
	PremiumRequests int
	ExitCode        int
	SessionID       string // the conversation it ran in ("" when the CLI cannot name one)
}

// Status describes whether a driver can run on this machine.
type Status struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Problem   string `json:"problem,omitempty"`
	Billing   string `json:"billing"` // usd | premium | none
}

// Driver runs one agent CLI.
type Driver interface {
	Name() string
	Status(ctx context.Context) Status
	Run(ctx context.Context, req Request, emit func(Event)) (Result, error)
}

// ErrNotInstalled is returned when the CLI binary is not on PATH.
var ErrNotInstalled = errors.New("agent CLI not found on PATH")

// Registry holds the available drivers by name.
type Registry struct{ m map[string]Driver }

// NewRegistry returns a registry with Claude, Copilot and the mock.
func NewRegistry() *Registry {
	r := &Registry{m: map[string]Driver{}}
	r.Add(NewClaude())
	r.Add(NewCopilot())
	r.Add(NewMock())
	return r
}

// Add registers d (replacing any driver of the same name).
func (r *Registry) Add(d Driver) { r.m[d.Name()] = d }

// Get looks up a driver.
func (r *Registry) Get(name string) (Driver, error) {
	if d, ok := r.m[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("unknown agent %q", name)
}

// Names lists registered driver names.
func (r *Registry) Names() []string {
	var n []string
	for k := range r.m {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func findBinary(names ...string) (string, error) {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", ErrNotInstalled
}

func version(ctx context.Context, bin string) string {
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// toolTarget picks the most useful argument to show next to a tool name.
func toolTarget(args map[string]any) string {
	for _, k := range []string{"file_path", "path", "pattern", "command", "name", "url", "query"} {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
