package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"codegen/internal/builtin"
	"codegen/internal/extension"
)

// Built-in tools as flow steps. A tool step (or an output's "call a tool"
// action) can run one of SpecWright's built-in tools directly — read a
// file, search the code, list changes, run a command, fetch a web page —
// without the AI. In a flow they are named "builtin:<name>"; project tool
// names cannot contain ":", so the two never collide. Tools switched off
// by the machine policy are not offered and do not run.

// BuiltinPrefix marks a built-in tool in a flow.
const BuiltinPrefix = "builtin:"

// notInFlows are built-ins that only make sense inside an agent's own loop.
var notInFlows = map[string]bool{
	"todo_list":          true, // the agent's scratch list
	"get_command_output": true, // follows up on a background run_command
}

// FlowBuiltinTools lists the built-in tools a flow can use, shaped like
// project tools (name, description, parameters) for the designer.
func (h *Hub) FlowBuiltinTools() []extension.Extension {
	off := h.disabledBuiltins()
	var out []extension.Extension
	for _, t := range builtin.Tools() {
		if off[t.Name] || notInFlows[t.Name] {
			continue
		}
		out = append(out, builtinAsTool(t))
	}
	return out
}

func builtinAsTool(t builtin.Tool) extension.Extension {
	e := extension.Extension{Name: BuiltinPrefix + t.Name, Desc: t.Title + ": " + t.Desc, Kind: "builtin", Scope: "builtin",
		Status: "tested", Approved: true, Lang: t.Group, Params: []extension.Param{}, Secrets: []extension.Secret{}, Perms: []string{}, OutputBy: t.OutputBy}
	if !t.ReadOnly {
		e.Perms = []string{"writes"}
	}
	props, _ := t.Schema["properties"].(map[string]any)
	req := map[string]bool{}
	switch r := t.Schema["required"].(type) {
	case []string:
		for _, n := range r {
			req[n] = true
		}
	case []any:
		for _, n := range r {
			req[fmt.Sprint(n)] = true
		}
	}
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	// required first, then alphabetical
	sort.Slice(names, func(i, j int) bool {
		if req[names[i]] != req[names[j]] {
			return req[names[i]]
		}
		return names[i] < names[j]
	})
	for _, n := range names {
		p, _ := props[n].(map[string]any)
		typ, _ := p["type"].(string)
		desc, _ := p["description"].(string)
		switch typ {
		case "array":
			desc += " (one per line, or a JSON array)"
		case "object":
			desc += " (JSON)"
		}
		var enum []string
		switch ev := p["enum"].(type) {
		case []string:
			enum = ev
		case []any:
			for _, v := range ev {
				enum = append(enum, fmt.Sprint(v))
			}
		}
		e.Params = append(e.Params, extension.Param{Name: n, Type: typ, Required: req[n], Desc: desc, Enum: enum})
	}
	return e
}

// IsBuiltinTool says whether a flow tool name is a built-in.
func IsBuiltinTool(name string) bool { return strings.HasPrefix(name, BuiltinPrefix) }

// flowBuiltin finds a built-in tool a flow may use.
func (h *Hub) flowBuiltin(name string) (*extension.Extension, error) {
	bare := strings.TrimPrefix(name, BuiltinPrefix)
	t, ok := builtin.Find(bare)
	if !ok || notInFlows[bare] {
		return nil, fmt.Errorf("there is no built-in tool %s", bare)
	}
	if h.disabledBuiltins()[bare] {
		return nil, fmt.Errorf("%s is turned off on this computer by policy", bare)
	}
	e := builtinAsTool(t)
	return &e, nil
}

// builtinArgs turns a flow's text arguments into the types the tool's
// schema asks for: numbers, booleans, lists and objects. Empty fields are
// left out so the tool's defaults apply.
func builtinArgs(e *extension.Extension, in map[string]any) (map[string]any, error) {
	types := map[string]string{}
	for _, p := range e.Params {
		types[p.Name] = p.Type
	}
	out := map[string]any{}
	for k, v := range in {
		s, isStr := v.(string)
		if !isStr {
			out[k] = v // already typed (from the AI)
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		switch types[k] {
		case "integer", "number":
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("%s must be a number, not %q", k, clipLine(s, 40))
			}
			out[k] = n
		case "boolean":
			b, err := strconv.ParseBool(strings.ToLower(s))
			if err != nil {
				switch strings.ToLower(s) {
				case "yes", "y", "on":
					b, err = true, nil
				case "no", "n", "off":
					b, err = false, nil
				}
			}
			if err != nil {
				return nil, fmt.Errorf("%s must be true or false, not %q", k, clipLine(s, 40))
			}
			out[k] = b
		case "array":
			if strings.HasPrefix(s, "[") {
				var a []any
				if err := json.Unmarshal([]byte(s), &a); err != nil {
					return nil, fmt.Errorf("%s is not a valid JSON array: %v", k, err)
				}
				out[k] = a
				continue
			}
			var a []any
			for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
				if line = strings.TrimSpace(line); line != "" {
					a = append(a, line)
				}
			}
			out[k] = a
		case "object":
			var o map[string]any
			if err := json.Unmarshal([]byte(s), &o); err != nil {
				return nil, fmt.Errorf("%s must be a JSON object: %v", k, err)
			}
			out[k] = o
		default:
			out[k] = v // strings keep their spacing
		}
	}
	return out, nil
}

// runFlowBuiltin runs a built-in tool for a flow step.
func (h *Hub) runFlowBuiltin(ctx context.Context, e *extension.Extension, args map[string]any, timeout time.Duration) extension.RunResult {
	start := time.Now()
	typed, err := builtinArgs(e, args)
	if err != nil {
		return extension.RunResult{Error: err.Error(), ExitCode: 1, DurationMS: time.Since(start).Milliseconds()}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	out, err := h.tools.Call(ctx, strings.TrimPrefix(e.Name, BuiltinPrefix), typed)
	out = h.Store.Redactor().String(out)
	res := extension.RunResult{OK: err == nil, Output: out, DurationMS: time.Since(start).Milliseconds(), Command: e.Name}
	if err != nil {
		res.ExitCode = 1
		res.Error = h.Store.Redactor().String(err.Error())
	}
	return res
}
