package core

import (
	"codegen/internal/bigout"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
	"codegen/internal/store"
)

// A tool step can be given a free-form request instead of (or as well as)
// exact arguments. Before the tool runs, the agent investigates the project
// — schema files, migrations, models, docs — and may call the tool itself
// to explore (for example to list a database's tables), then answers with
// the arguments as JSON. SpecWright checks them and runs the tool with
// them, so the step's output is the tool's real output. Arguments the user
// filled in stay fixed.

// AIArgs is what the agent decided for a tool step.
type AIArgs struct {
	Arguments map[string]any `json:"arguments"`
	Why       string         `json:"why"`
}

// toolForFlow finds a callable tool by name.
func (h *Hub) toolForFlow(name string) (*extension.Extension, error) {
	if IsBuiltinTool(name) {
		return h.flowBuiltin(name)
	}
	for _, x := range h.Callable() {
		if x.Name == name {
			x := x
			return &x, nil
		}
	}
	return nil, fmt.Errorf("the tool %s is not available or not approved", name)
}

// aiToolArgs asks the agent to build a tool's arguments from a request.
func (h *Hub) aiToolArgs(ctx context.Context, root string, fr *flowRun, n *flow.Node, request string, fixed map[string]any, d flow.Data) (map[string]any, string, store.Usage, error) {
	var u store.Usage
	e, err := h.toolForFlow(n.Tool)
	if err != nil {
		return nil, "", u, err
	}
	prompt := aiArgsPrompt(e, request, fixed, d) + h.aiSuffix(fr.flow, d)
	h.flowPublish(fr, "flow.args", "Working out the arguments for "+e.Name, map[string]any{"_step": n.ID, "tool": e.Name, "request": request})
	ans, u, err := h.flowAgent(ctx, root, fr, n.ID, prompt, fr.st.Options.Model)
	if err != nil {
		return nil, "", u, fmt.Errorf("the agent could not build the arguments: %v", err)
	}
	got, err := parseAIArgs(ans)
	if err != nil {
		// one more try, pointing at the problem
		retry := prompt + "\n\nYour previous reply could not be used (" + err.Error() + "). Reply again with ONLY the JSON object."
		ans2, u2, err2 := h.flowAgent(ctx, root, fr, n.ID, retry, fr.st.Options.Model)
		u.Add(u2)
		if err2 != nil {
			return nil, "", u, err2
		}
		if got, err = parseAIArgs(ans2); err != nil {
			return nil, "", u, fmt.Errorf("the agent's reply did not contain the arguments as JSON: %v", err)
		}
	}
	args := map[string]any{}
	for k, v := range got.Arguments {
		args[k] = v
	}
	for k, v := range fixed { // what the user filled in wins
		args[k] = v
	}
	known := map[string]bool{}
	for _, p := range e.Params {
		known[p.Name] = true
		if p.Required {
			if v, ok := args[p.Name]; !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
				return nil, got.Why, u, fmt.Errorf("the agent left the required parameter %q empty", p.Name)
			}
		}
	}
	for k := range args {
		if !known[k] {
			delete(args, k) // the tool does not take it
		}
	}
	h.flowPublish(fr, "flow.args", "Arguments for "+e.Name+": "+argSummary(args), map[string]any{"_step": n.ID, "tool": e.Name, "args": args, "why": got.Why})
	return args, got.Why, u, nil
}

func aiArgsPrompt(e *extension.Extension, request string, fixed map[string]any, d flow.Data) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are preparing one call to the tool %q: %s\n\n", e.Name, e.Desc)
	b.WriteString("Its parameters:\n")
	for _, p := range e.Params {
		req := "optional"
		if p.Required {
			req = "required"
		}
		fmt.Fprintf(&b, "- %s (%s, %s): %s\n", p.Name, firstNonEmpty(p.Type, "string"), req, p.Desc)
		if p.Schema != nil && p.Schema.Known() { // a JSON parameter: its shape
			fmt.Fprintf(&b, "  shape: %s\n", p.Schema.Compact())
		}
	}
	fmt.Fprintf(&b, "\nWhat the call should achieve:\n%s\n", strings.TrimSpace(request))
	if len(fixed) > 0 {
		fb, _ := json.Marshal(fixed)
		fmt.Fprintf(&b, "\nThese arguments are fixed by the user; keep them exactly: %s\n", fb)
	}
	if in := strings.TrimSpace(d.Input); in != "" {
		if len(in) > 4000 {
			in = bigout.SafeCut(in, 4000) + "\n…"
		}
		fmt.Fprintf(&b, "\nOutput of the previous step (you may use it):\n```\n%s\n```\nIt is also saved in a file: for a parameter that expects a file path to this data, use the exact value {{input_file}}.\n", in)
	}
	if len(d.Files) > 0 {
		ids := make([]string, 0, len(d.Files))
		for id := range d.Files {
			if id != "start" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			b.WriteString("\nEarlier steps saved their output in files. For a parameter that expects the path of one of them, use its value exactly as written here:\n")
			for _, id := range ids {
				fmt.Fprintf(&b, "- step %s: {{%s.output_file}}\n", id, id)
			}
		}
	}
	if len(d.Inputs) > 0 {
		keys := make([]string, 0, len(d.Inputs))
		for k := range d.Inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nThe flow's inputs:\n")
		for _, k := range keys {
			v := d.Inputs[k]
			if len(v) > 500 {
				v = bigout.SafeCut(v, 500) + "…"
			}
			fmt.Fprintf(&b, "- %s: %s\n", k, v)
		}
	}
	fmt.Fprintf(&b, `
Investigate before you decide: read the project's files that describe what you need (for a database query: schema files, migrations, ORM models, seed data; for an API: specs and clients) with your file tools. If that is not enough, you may call %q yourself through the project tool to explore (for example, list the tables or columns of a database) — explore only, read-only.

Then reply with ONLY a JSON object, no code fences and no other text:
{"arguments": {<parameter>: <value>, ...}, "why": "<one sentence on how you chose them>"}`, e.Name)
	return b.String()
}

// parseAIArgs pulls {"arguments": …} out of an agent's reply.
func parseAIArgs(ans string) (AIArgs, error) {
	s := strings.TrimSpace(ans)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	// the last complete JSON object that has "arguments"
	for end := strings.LastIndex(s, "}"); end >= 0; end = strings.LastIndex(s[:end], "}") {
		for start := strings.LastIndex(s[:end+1], "{"); start >= 0; start = strings.LastIndex(s[:start], "{") {
			var a AIArgs
			if json.Unmarshal([]byte(s[start:end+1]), &a) == nil && a.Arguments != nil {
				return a, nil
			}
			if start == 0 {
				break
			}
		}
		if end == 0 {
			break
		}
	}
	return AIArgs{}, errors.New("no JSON object with \"arguments\" found")
}
