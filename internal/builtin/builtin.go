// Package builtin is SpecWright's set of built-in agent tools: fast,
// workspace-confined file, search, edit, command and web operations,
// modelled on the editor tools agents expect (read a file, edit a file,
// search text, list a directory, …). They are compiled into the app, so no
// user can change them, and each is exposed as its own MCP tool by the
// agentbridge server, available to every workflow step and playground
// prompt.
//
// Results are plain text shaped for language models: line-numbered file
// reads, "path:line: text" search hits grouped by file, explicit
// truncation notices, and errors that say how to fix the call.
package builtin

import (
	"codegen/internal/flow"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tool is one built-in tool.
type Tool struct {
	Name     string         `json:"name"`
	Title    string         `json:"title"`
	Group    string         `json:"group"` // read | search | edit | execute | web | plan
	Desc     string         `json:"description"`
	Schema   map[string]any `json:"inputSchema"`
	ReadOnly bool           `json:"readOnly"`
	// OutputBy is the shape of the output when an argument has a given
	// value (argument → value → shape): fetch_webpage with format=json.
	// Flow designer metadata; never sent to the AI.
	OutputBy map[string]map[string]*flow.Schema `json:"outputBy,omitempty"`
	run      func(ctx context.Context, b *Box, a args) (string, error)
}

// Box holds the state the tools share for one app: the workspace they are
// confined to, the environment commands run with, background commands and
// the todo list.
type Box struct {
	mu       sync.Mutex
	root     string
	env      func() []string
	trash    string
	problems func(ctx context.Context) (string, string, error) // command, output, err
	procs    map[string]*proc
	nextProc int
	todos    map[string][]Todo // per workspace
	cwd      func() string     // run_command's folder when none is given (workspace-relative)
}

// Config wires a Box to the app.
type Config struct {
	Env      func() []string                                   // extra environment for commands
	Trash    string                                            // where delete_path moves things
	Problems func(ctx context.Context) (string, string, error) // build/compile for get_problems
	// DefaultCwd is where run_command runs when the call names no cwd
	// (workspace-relative; "" = the root), e.g. the Spec Kit project folder
	// of the step in progress.
	DefaultCwd func() string
}

// New returns a Box.
func New(c Config) *Box {
	return &Box{env: c.Env, trash: c.Trash, problems: c.Problems, cwd: c.DefaultCwd, procs: map[string]*proc{}, todos: map[string][]Todo{}}
}

// SetRoot points the tools at a workspace.
func (b *Box) SetRoot(root string) { b.mu.Lock(); b.root = root; b.mu.Unlock() }

// Root is the current workspace.
func (b *Box) Root() string { b.mu.Lock(); defer b.mu.Unlock(); return b.root }

// Tools lists every built-in tool in a stable order.
func Tools() []Tool { return registry }

// Find looks a tool up by name.
func Find(name string) (Tool, bool) {
	for _, t := range registry {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// Call runs a tool by name with JSON-like arguments.
func (b *Box) Call(ctx context.Context, name string, raw map[string]any) (string, error) {
	t, ok := Find(name)
	if !ok {
		return "", fmt.Errorf("unknown built-in tool %q", name)
	}
	if b.Root() == "" {
		return "", errors.New("no workspace is open in SpecWright")
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return t.run(ctx, b, args(raw))
}

// --- arguments ---------------------------------------------------------------

type args map[string]any

func (a args) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func (a args) need(k string) (string, error) {
	v := a.str(k)
	if v == "" {
		return "", fmt.Errorf("argument %q is required", k)
	}
	return v, nil
}

func (a args) num(k string, def, lo, hi int) int {
	n := def
	switch v := a[k].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case json.Number:
		if i, err := v.Int64(); err == nil {
			n = int(i)
		}
	case string:
		_, _ = fmt.Sscan(v, &n)
	}
	if n < lo {
		n = lo
	}
	if hi > 0 && n > hi {
		n = hi
	}
	return n
}

func (a args) flag(k string) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	}
	return false
}

func (a args) strs(k string) []string {
	switch v := a[k].(type) {
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// --- workspace sandbox ------------------------------------------------------

// resolve maps a tool path (relative to the workspace, or absolute inside
// it) to an absolute path, refusing anything outside the workspace,
// including through symlinks.
func (b *Box) resolve(p string) (abs, rel string, err error) {
	root := b.Root()
	if root == "" {
		return "", "", errors.New("no workspace is open")
	}
	p = strings.TrimSpace(p)
	if p == "" || p == "." || p == "/" {
		p = "."
	}
	var cand string
	if filepath.IsAbs(p) {
		cand = filepath.Clean(p)
	} else {
		cand = filepath.Join(root, filepath.FromSlash(p))
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if realRoot == "" {
		realRoot = root
	}
	check := cand
	// for a path that does not exist yet, check its nearest existing parent
	for {
		if _, err := os.Lstat(check); err == nil {
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	real, err := filepath.EvalSymlinks(check)
	if err != nil {
		real = check
	}
	if !within(realRoot, real) && !within(root, cand) {
		return "", "", fmt.Errorf("%s is outside the workspace; use a path relative to the workspace root", p)
	}
	if !within(realRoot, real) {
		return "", "", fmt.Errorf("%s resolves (through a symlink) outside the workspace", p)
	}
	r, _ := filepath.Rel(root, cand)
	return cand, filepath.ToSlash(r), nil
}

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// --- small helpers ------------------------------------------------------------

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func p(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func arr(item map[string]any, desc string) map[string]any {
	return map[string]any{"type": "array", "items": item, "description": desc}
}

// Close stops background commands.
func (b *Box) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pr := range b.procs {
		pr.cancel()
	}
}

var _ = time.Now
