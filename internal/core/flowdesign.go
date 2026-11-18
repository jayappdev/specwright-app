package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
	"codegen/internal/extension"
	"codegen/internal/flow"
	"codegen/internal/store"
)

// patterns compiled once (not on every call)
var (
	notSlugChars  = regexp.MustCompile(`[^A-Za-z0-9_-]+`) // characters not allowed in an id
	versionSuffix = regexp.MustCompile(`\s*\(v\d+\)$`)    // a " (v2)" version suffix of a flow name
)

// The AI designer: someone describes what they want in their own words and
// gets a few ready-made flows to choose from — or asks for an existing flow
// to be improved, and saves the result as a new version. The agent is given
// the project's tools with their contracts (parameters and output shapes)
// and the step types, and is told to prefer, in order: existing tools,
// deterministic steps that connect them by their shapes, and only then AI
// steps. Every option it returns is checked here (tools exist, the flow
// validates); options with errors get one repair round, and the options are
// ranked deterministic-first.

// DesignRequest asks the AI designer for flows.
type DesignRequest struct {
	Prompt  string `json:"prompt"`
	FlowID  string `json:"flowId,omitempty"`  // improve this flow (new versions of it)
	Options int    `json:"options,omitempty"` // how many options (2–4, default 3)
	Model   string `json:"model,omitempty"`
	// Base: improve this unsaved flow (a wizard draft) instead of FlowID
	Base *flow.Flow `json:"-"`
	// Min: the fewest options (default 2); the wizard asks for one revision
	Min int `json:"-"`
}

// FlowOption is one design to choose from.
type FlowOption struct {
	Title         string       `json:"title"`
	Summary       string       `json:"summary"`
	Why           string       `json:"why,omitempty"`
	Flow          flow.Flow    `json:"flow"`
	Issues        []flow.Issue `json:"issues"`
	Errors        int          `json:"errors"`
	AISteps       int          `json:"aiSteps"`
	Tools         []string     `json:"tools"`
	Deterministic bool         `json:"deterministic"` // no AI steps
	Recommended   bool         `json:"recommended,omitempty"`
	Changes       []string     `json:"changes,omitempty"` // improving: what differs from the current flow
	Kept          int          `json:"kept,omitempty"`    // improving: steps of the current flow it keeps
	Rewrite       bool         `json:"rewrite,omitempty"` // improving: it replaces most of the current flow
	Fixes         int          `json:"fixes,omitempty"`   // improving: problems of the current flow it fixes
	// Test is the sample data the option was tried with, and Trial how the
	// try went (tools and AI mocked, everything else run for real).
	Test  flow.DryTest    `json:"test"`
	Trial *flow.DryResult `json:"trial,omitempty"`
}

// DesignResult is the AI designer's answer.
type DesignResult struct {
	Options  []FlowOption `json:"options"`
	Usage    store.Usage  `json:"usage"`
	Repaired bool         `json:"repaired,omitempty"`
	Notes    []string     `json:"notes,omitempty"`
	// Raw: the agent's answers as they came (the flow wizard logs them)
	Raw []string `json:"-"`
}

// DesignFlows asks the agent for flow designs and checks them.
func (h *Hub) DesignFlows(r DesignRequest) (DesignResult, error) {
	root, err := h.need()
	if err != nil {
		return DesignResult{}, err
	}
	r.Prompt = strings.TrimSpace(r.Prompt)
	base := r.Base
	if r.FlowID != "" && base == nil {
		if base, err = flow.Load(root, r.FlowID); err != nil {
			return DesignResult{}, err
		}
	}
	if r.Prompt == "" && base == nil {
		return DesignResult{}, errors.New("describe what the flow should do")
	}
	if r.Min < 1 {
		r.Min = 2
	}
	if r.Options < r.Min || r.Options > 4 {
		r.Options = max(r.Min, 3)
		if r.Min == 1 && r.Options > 1 {
			r.Options = 1
		}
	}
	tools := h.designTools()
	var baseIssues []flow.Issue
	if base != nil {
		baseIssues = h.ValidateFlow(*base)
	}
	prompt := designPrompt(r, base, baseIssues, tools)
	var res DesignResult
	h.designProgress("Reading the tools and their contracts")
	text, u, err := h.designAgent(root, prompt, r.Model)
	res.Usage.Add(u)
	res.Raw = append(res.Raw, text)
	if err != nil {
		return res, err
	}
	h.designProgress("Checking the designs")
	opts, err := parseDesigns(text)
	if err != nil { // one more try, pointing at the problem, rather than losing the work
		h.designProgress("The answer could not be read; asking again")
		text2, u2, err2 := h.designAgent(root, prompt+"\n\nYour previous reply could not be used ("+err.Error()+"). Reply again with ONLY the JSON object: input sample values as strings.", r.Model)
		res.Usage.Add(u2)
		res.Raw = append(res.Raw, text2)
		if err2 != nil {
			return res, err
		}
		if opts, err = parseDesigns(text2); err != nil {
			return res, err
		}
	}
	res.Options = h.checkDesigns(opts, base, tools, &res.Notes)
	// one repair round for the options that do not validate
	var bad []int
	for i, o := range res.Options {
		if o.Errors > 0 {
			bad = append(bad, i)
		}
	}
	if len(bad) > 0 {
		h.designProgress("Fixing the designs that do not check out")
		text, u, err := h.designAgent(root, repairPrompt(res.Options, bad), r.Model)
		res.Usage.Add(u)
		res.Raw = append(res.Raw, text)
		if err != nil {
			res.Notes = append(res.Notes, "The repair round failed: "+err.Error())
		} else if fixed, perr := parseDesigns(text); perr != nil {
			res.Notes = append(res.Notes, "The repair round's answer could not be read: "+perr.Error())
		} else {
			{
				fixedOpts := h.checkDesigns(fixed, base, tools, &res.Notes)
				for k, i := range bad {
					if k < len(fixedOpts) && fixedOpts[k].Errors <= res.Options[i].Errors {
						res.Options[i] = fixedOpts[k]
						res.Repaired = true
					}
				}
			}
		}
	}
	rankDesigns(res.Options)
	h.recordDesignUsage(root, res.Usage, r.Model)
	return res, nil
}

// designTool is a tool as the designer describes it to the agent.
type designTool struct {
	Name, Desc string
	Params     []extension.Param
	Output     *flow.Schema
	OutputBy   map[string]map[string]*flow.Schema
	Builtin    bool
}

// designTools are the tools a flow can use (as flowTools), with their
// contracts.
func (h *Hub) designTools() []designTool {
	var out []designTool
	stats, _ := h.Store.ToolStats(h.Root(), "")
	if exts, err := h.Extensions(); err == nil {
		for _, e := range exts {
			if extension.IsManagedScope(e.Scope) && (e.Managed == nil || e.Managed.Status != extension.ManagedVerified) {
				continue
			}
			if !e.Approved && !extension.IsManagedScope(e.Scope) {
				continue // not runnable yet
			}
			st, ok := stats[e.Name]
			out = append(out, designTool{Name: e.Name, Desc: e.Desc + reliabilityNote(st, ok), Params: e.Params, Output: e.Output, OutputBy: e.OutputBy})
		}
	}
	for _, e := range h.FlowBuiltinTools() {
		out = append(out, designTool{Name: e.Name, Desc: e.Desc, Params: e.Params, Output: e.Output, OutputBy: e.OutputBy, Builtin: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Builtin && out[j].Builtin })
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

const designRules = `You design automation flows for the SpecWright Flow designer. A flow is a graph of steps; data flows along the connections (edges), each step getting the output of the steps connected into it.

Design rules, in order of preference:
1. Use the project's existing TOOLS first. Wire them by their contracts: give each tool argument a value ({{inputs.<name>}}, {{<step id>.buffer_output}}, {{<step id>.buffer_output.json.<path>}} or {{input_file}}), and read a tool's JSON output by the fields of its output shape.
2. Connect and shape the data with DETERMINISTIC steps: transform, filter, aggregate, decision, loop/gather, merge, output.
3. Use an "ai" step only for what no tool or deterministic step can do (judgement, prose, classification, summarising). When later steps read its fields, give it an "outputSchema" (a JSON Schema) so it replies with that JSON.
4. Keep flows small and correct; never invent tools: use only the tool names listed below.
5. REUSABLE: a value the user may vary (a project, a status, a component, a label, an id, a name, a date range, a path, a URL, a limit) is a flow INPUT with the request's value as its "default", passed into the tool arguments as {{inputs.<name>}} — never a constant in a tool argument, an ai prompt, a transform, a filter or a command. A flow asked for "project ABC, status Done" has inputs project (default "ABC") and status (default "Done"), so the same flow serves other projects and statuses. Small steps that each do one job, connected, over one step that does everything.

Step types (node JSON; every node has "id" and "type" and may have "title"):
- start: the flow's inputs; always present with id "start", nothing feeds it.
- tool: {"tool": "<name>", "args": {"<param>": "<template>"}}
- ai: {"prompt": "<template>", "outputSchema": <JSON Schema, optional>}
- improve ("Improve until done"): {"pair": "before"|"after", "goal": "<when the result is good enough>", "prompt": "<pair after: what to produce; before: optional instructions>", "rounds": 5, "approve": "none"|"done"|"each"}. Pair "before": exactly one tool step connects into it; it judges that tool's result and runs it again with changed arguments until done. Pair "after": it produces a result and connects to exactly one tool step that checks it (tests, linter, validator); the findings go back to it until done. Use it for fix-and-check work.
- transform: {"op": "<op>", "expr": "...", "template": "..."}; ops: ` + "%s" + `. json-path: expr is a path like items[*].id. json-template: template is the JSON to produce with {{this.<path>}} placeholders and {"{{#each this.<list>}}": {...}} blocks; expr is an optional list path to map over.
- filter: {"mode": "keep"|"drop", "cond": <condition>, "orderBy": ["-total"], "max": 10}; works on rows (a JSON array).
- aggregate: {"mode": "group"|"window", "groupBy": ["region"], "measures": [{"fn": "count|count_distinct|sum|avg|min|max|median|first|last|list|distinct|concat|row_number|rank|dense_rank|running_sum|running_count|running_avg|lag|lead|pct_of_total|ntile", "field": "total", "as": "revenue"}], "orderBy": ["-revenue"]}
- decision: {"mode": "first"|"all", "branches": [{"id": "big", "label": "Big order", "when": <condition>}]}; edges out of it carry "branch": "<branch id>" or "else".
- loop: {"split": "auto|lines|json|csv|regex", "field": "<the path of the list when its input is a JSON object, e.g. the input's name when connected to start>"}; the steps after it run once per item ({{item}}, {{item.json.<path>}}, or {{<loop id>.item}} for nested loops) until a gather.
- gather: {"mode": "concat|sections|json-array|json-object|template"}; ends the loop.
- merge: {"mode": "concat|json-object|json-array|template"}; combines several steps.
- output: {"format": "md|txt|html|csv|json", "template": "<optional>", "actions": [{"type": "show"}, {"type": "save", "path": "out/report.md"}]}; ends a path.
Condition: {"match": "all|any|none", "rules": [{"field": "status", "op": "eq", "value": "open"}, {"group": <condition>}]}; ops: eq ne gt gte lt lte contains not_contains starts ends matches in not_in between empty not_empty exists not_exists true false len_eq len_gt len_lt. Fields are paths into the data (customer.tier, items[*].qty).
Edge: {"from": "<id>", "to": "<id>", "branch": "<only out of a decision>"}.
Inputs: [{"name": "ticket", "label": "Ticket", "type": "text|multiline|secret|number|boolean|date|datetime|choice|multichoice|file|files|clipboard", "required": true, "options": ["a","b"]}]. Passwords, API keys and tokens: type "secret" (no default), given to tools as {{inputs.<name>}} in their args, never to AI steps.
Refer to steps by id: {{<step id>.buffer_output}} (its text), {{<step id>.buffer_output.json.<path>}} (a field of its JSON output, e.g. an ai step's outputSchema field). There is no {{<step>.output}}.
Give every optional input that a required tool argument uses a "default" (e.g. "." for a folder), so the flow runs when the input is left empty. Step ids: letters, digits, - or _, starting with a letter.`

func designPrompt(r DesignRequest, base *flow.Flow, baseIssues []flow.Issue, tools []designTool) string {
	var ops []string
	for k := range flow.Ops {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	var b strings.Builder
	fmt.Fprintf(&b, designRules, strings.Join(ops, ", "))
	b.WriteString("\n\nTOOLS (name: what it does; args; output):\n")
	if len(tools) == 0 {
		b.WriteString("(none: use deterministic steps and ai steps)\n")
	}
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s: %s", t.Name, clipText(t.Desc, 160))
		var ps []string
		for _, p := range t.Params {
			s := p.Name + " " + firstNonEmpty(p.Type, "string")
			if !p.Required {
				s += "?"
			}
			if p.Schema != nil && p.Schema.Known() {
				s += " " + p.Schema.Compact()
			}
			ps = append(ps, s)
		}
		if len(ps) > 0 {
			fmt.Fprintf(&b, "; args: %s", strings.Join(ps, ", "))
		}
		if t.Output != nil && t.Output.Known() {
			fmt.Fprintf(&b, "; output: %s", t.Output.Compact())
		} else {
			b.WriteString("; output: text")
		}
		for p, m := range t.OutputBy {
			for v, sh := range m {
				fmt.Fprintf(&b, "; with %s=%q the output is JSON: %s", p, v, sh.Compact())
			}
		}
		b.WriteString("\n")
	}
	if base != nil {
		c := *base
		c.Nodes = append([]flow.Node(nil), base.Nodes...)
		for i := range c.Nodes {
			c.Nodes[i].X, c.Nodes[i].Y = 0, 0
		}
		cur, _ := json.Marshal(map[string]any{"name": c.Name, "inputs": c.Inputs, "nodes": c.Nodes, "edges": c.Edges})
		fmt.Fprintf(&b, "\nCURRENT FLOW:\n%s\n", cur)
		if len(baseIssues) > 0 {
			b.WriteString("\nPROBLEMS IN THE CURRENT FLOW (fix these first):\n")
			for _, is := range baseIssues {
				fmt.Fprintf(&b, "- %s: %s\n", is.Level, is.Message)
			}
		}
		fmt.Fprintf(&b, "\nWHAT TO IMPROVE:\n%s\n", firstNonEmpty(r.Prompt, "Fix its problems, then make it more reliable: use tools and deterministic steps where it uses AI, check results, and handle the cases it misses."))
		fmt.Fprintf(&b, `
This is a FIX of the current flow, not a new design. Each option must be the current flow with changes:
- keep its purpose, its inputs and its outputs; keep every step that still fits, with the same id;
- fix the problems listed above first (e.g. a tool that does not exist: replace it with a listed tool that does the job);
- change only what is needed and say what you changed in "why".
Give up to %d different fixed or improved versions of it.`, r.Options)
	} else {
		fmt.Fprintf(&b, "\nWHAT THE USER WANTS:\n%s\n", r.Prompt)
		fmt.Fprintf(&b, "\nGive %d different options: if possible one with no ai steps at all, and one that uses AI where it clearly helps.", r.Options)
	}
	b.WriteString(`

Each option is TRIED before the user sees it: tools and ai steps are not called but replaced by your mocks, and every other step runs for real on them. So give each option a "test": realistic sample "inputs"; "mocks" with a realistic sample output for EVERY tool and ai step (by step id; JSON matching the tool's output shape or the ai step's outputSchema, or text); and "expect": what the output steps must give with that data ("notEmpty", "contains": words that must appear, "fields": JSON paths that must exist). Make the mocks exercise the main path (e.g. a filter must find rows).

Reply with ONLY this JSON, no explanation and no code fences:
{"options": [{"title": "<short name>", "summary": "<one sentence: what it does>", "why": "<why this design: which tools and why AI or not>", "flow": {"name": "<flow name>", "inputs": [...], "nodes": [...], "edges": [...]}, "test": {"inputs": {"<input>": "<sample>"}, "mocks": {"<step id>": <sample output>}, "expect": [{"step": "<output step id>", "notEmpty": true, "contains": ["..."], "fields": ["..."]}]}}]}`)
	return b.String()
}

func repairPrompt(opts []FlowOption, bad []int) string {
	var b strings.Builder
	b.WriteString("These flow designs have problems: they do not validate, or their trial run with the sample data failed (tools and ai steps mocked, the other steps run for real). Fix each one — the flow, or its test if the mocks or expectations were wrong — keeping its idea and using only the tools listed before, and reply with ONLY JSON {\"options\": [...]} with the fixed options (each with title, summary, why, flow and test), in the same order.\n")
	for _, i := range bad {
		o := opts[i]
		fj, _ := json.Marshal(map[string]any{"flow": map[string]any{"name": o.Flow.Name, "inputs": o.Flow.Inputs, "nodes": stripXY(o.Flow.Nodes), "edges": o.Flow.Edges}, "test": o.Test})
		fmt.Fprintf(&b, "\nOPTION %q:\n%s\nPROBLEMS:\n", o.Title, fj)
		for _, is := range o.Issues {
			if is.Level == "error" {
				fmt.Fprintf(&b, "- %s\n", is.Message)
			}
		}
	}
	return b.String()
}

func stripXY(ns []flow.Node) []flow.Node {
	out := append([]flow.Node(nil), ns...)
	for i := range out {
		out[i].X, out[i].Y = 0, 0
	}
	return out
}

type rawDesign struct {
	Title   string          `json:"title"`
	Summary string          `json:"summary"`
	Why     string          `json:"why"`
	Flow    json.RawMessage `json:"flow"`
	Test    flow.DryTest    `json:"test"`
}

func parseDesigns(text string) ([]rawDesign, error) {
	var got struct {
		Options []rawDesign `json:"options"`
	}
	if err := json.Unmarshal([]byte(firstJSON(jsonReply(text))), &got); err != nil || len(got.Options) == 0 {
		return nil, fmt.Errorf("the agent did not return flow designs (%v)", firstNonEmpty(fmt.Sprint(err), "no options"))
	}
	return got.Options, nil
}

var nodeIDRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// checkDesigns turns the agent's designs into flows and checks them.
func (h *Hub) checkDesigns(raw []rawDesign, base *flow.Flow, tools []designTool, notes *[]string) []FlowOption {
	known := map[string]bool{}
	dryTools := map[string]flow.DryTool{}
	for _, t := range tools {
		known[t.Name] = true
		dt := flow.DryTool{Output: t.Output, OutputBy: t.OutputBy}
		for _, p := range t.Params {
			if p.Required {
				dt.Required = append(dt.Required, p.Name)
			}
		}
		dryTools[t.Name] = dt
	}
	var out []FlowOption
	for _, r := range raw {
		f, fixes, err := flow.ParseDesigned(r.Flow)
		if err != nil { // said, not silently dropped
			*notes = append(*notes, fmt.Sprintf("The option %q could not be read as a flow and was left out: %v", firstNonEmpty(r.Title, "untitled"), err))
			continue
		}
		fixDesign(f, known)
		f.Name = firstNonEmpty(strings.TrimSpace(f.Name), r.Title, "Designed flow")
		f.Description = firstNonEmpty(f.Description, r.Summary)
		if base != nil {
			f.Context, f.Skills, f.MinutesSaved = base.Context, base.Skills, base.MinutesSaved
		}
		f.Layout()
		o := FlowOption{Title: firstNonEmpty(r.Title, f.Name), Summary: r.Summary, Why: r.Why, Flow: *f, Tools: []string{}, Test: r.Test}
		o.Issues = h.ValidateFlow(*f)
		if o.Issues == nil {
			o.Issues = []flow.Issue{}
		}
		for _, n := range fixes {
			o.Issues = append(o.Issues, flow.Issue{Level: "warning", Message: n})
		}
		for _, is := range o.Issues {
			if is.Level == "error" {
				o.Errors++
			}
		}
		// try it: tools and AI mocked with the sample data, the rest run for real
		if o.Errors == 0 {
			trial := flow.DryRunFlow(f, r.Test, dryTools)
			o.Trial = &trial
			if !trial.OK {
				var failed []string
				for _, c := range trial.Checks {
					if !c.OK {
						failed = append(failed, strings.TrimSpace(c.Label+": "+c.Note))
					}
				}
				o.Errors++
				o.Issues = append(o.Issues, flow.Issue{Level: "error", Message: "Trial run with sample data failed: " + strings.Join(failed, "; ")})
			}
		}
		seen := map[string]bool{}
		for _, n := range f.Nodes {
			if n.Type == "ai" || (n.Type == "transform" && n.Op == "ai") || strings.TrimSpace(n.Instruction) != "" {
				o.AISteps++
			}
			if n.Type == "tool" && !seen[n.Tool] {
				seen[n.Tool] = true
				o.Tools = append(o.Tools, n.Tool)
			}
		}
		o.Deterministic = o.AISteps == 0
		if base != nil {
			o.Changes = flowChanges(base, f)
			steps := 0
			for _, n := range base.Nodes {
				if n.Type == "start" {
					continue
				}
				steps++
				if f.Node(n.ID) != nil {
					o.Kept++
				}
			}
			o.Rewrite = steps > 0 && o.Kept*2 < steps
			if o.Rewrite {
				o.Issues = append(o.Issues, flow.Issue{Level: "warning", Message: fmt.Sprintf("This replaces most of the current flow (it keeps %d of its %d steps).", o.Kept, steps)})
			}
			before := 0
			for _, is := range h.ValidateFlow(*base) {
				if is.Level == "error" {
					before++
				}
			}
			if before > o.Errors {
				o.Fixes = before - o.Errors
			}
		}
		out = append(out, o)
	}
	return out
}

// fixDesign repairs what an agent commonly gets slightly wrong: step ids,
// tool names without (or with) "builtin:", connections to missing steps.
func fixDesign(f *flow.Flow, known map[string]bool) {
	f.ID = ""
	ids := map[string]string{}
	used := map[string]bool{}
	for i := range f.Nodes {
		n := &f.Nodes[i]
		old := n.ID
		id := n.ID
		if n.Type == "start" {
			id = "start"
			n.Title = firstNonEmpty(n.Title, "Inputs")
		} else if !nodeIDRE.MatchString(id) || id == "start" {
			clean := func(x string) string {
				return strings.Trim(notSlugChars.ReplaceAllString(x, "-"), "-")
			}
			id = clean(id) // "Blockers!" → "Blockers"
			if !nodeIDRE.MatchString(id) || id == "start" {
				id = clean(n.Title)
			}
			if !nodeIDRE.MatchString(id) || id == "start" {
				id = n.Type
			}
		}
		for k := 2; used[id]; k++ {
			id = fmt.Sprintf("%s%d", strings.TrimRight(id, "0123456789"), k)
		}
		used[id] = true
		ids[old] = id
		n.ID = id
		if n.Type == "tool" && !known[n.Tool] {
			switch {
			case known["builtin:"+n.Tool]:
				n.Tool = "builtin:" + n.Tool
			case known[strings.TrimPrefix(n.Tool, "builtin:")]:
				n.Tool = strings.TrimPrefix(n.Tool, "builtin:")
			}
		}
	}
	var edges []flow.Edge
	for _, e := range f.Edges {
		from, ok1 := ids[e.From]
		to, ok2 := ids[e.To]
		if !ok1 || !ok2 || from == to {
			continue
		}
		e.From, e.To = from, to
		edges = append(edges, e)
	}
	f.Edges = edges
	// a renamed step: its references follow
	for old, id := range ids {
		if old == id || old == "" {
			continue
		}
		re := regexp.MustCompile(`\{\{(\s*)(steps\.)?` + regexp.QuoteMeta(old) + `([.}\s])`)
		fix := func(s string) string { return re.ReplaceAllString(s, "{{${1}${2}"+id+"${3}") }
		for i := range f.Nodes {
			n := &f.Nodes[i]
			n.Prompt, n.Template, n.Source, n.Instruction = fix(n.Prompt), fix(n.Template), fix(n.Source), fix(n.Instruction)
			for k, v := range n.Args {
				n.Args[k] = fix(v)
			}
		}
	}
}

// flowChanges says what a new version changes: steps added, removed and
// changed.
func flowChanges(base, f *flow.Flow) []string {
	var out []string
	old := map[string]flow.Node{}
	for _, n := range base.Nodes {
		old[n.ID] = n
	}
	label := func(n flow.Node) string { return firstNonEmpty(n.Title, n.ID) }
	for _, n := range f.Nodes {
		o, ok := old[n.ID]
		if !ok {
			out = append(out, "Adds "+label(n)+" ("+n.Type+")")
			continue
		}
		delete(old, n.ID)
		a, b := o, n
		a.X, a.Y, b.X, b.Y = 0, 0, 0, 0
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			out = append(out, "Changes "+label(n))
		}
	}
	var gone []string
	for _, n := range old {
		gone = append(gone, "Removes "+label(n))
	}
	sort.Strings(gone)
	out = append(out, gone...)
	if len(f.Inputs) != len(base.Inputs) {
		out = append(out, fmt.Sprintf("Inputs: %d → %d", len(base.Inputs), len(f.Inputs)))
	}
	if len(out) == 0 {
		out = []string{"No changes to the steps"}
	}
	return out
}

// rankDesigns: designs that check out first, then those with fewer AI
// steps, then those using more of the project's tools.
func rankDesigns(opts []FlowOption) {
	sort.SliceStable(opts, func(i, j int) bool {
		a, b := opts[i], opts[j]
		if (a.Errors == 0) != (b.Errors == 0) {
			return a.Errors == 0
		}
		if a.Rewrite != b.Rewrite { // improving: changes to the flow before rewrites of it
			return !a.Rewrite
		}
		if a.Fixes != b.Fixes {
			return a.Fixes > b.Fixes
		}
		if a.AISteps != b.AISteps {
			return a.AISteps < b.AISteps
		}
		return len(a.Tools) > len(b.Tools)
	})
	for i := range opts {
		opts[i].Recommended = i == 0 && opts[i].Errors == 0 && !opts[i].Rewrite
	}
}

func (h *Hub) designProgress(msg string) {
	h.Bus.Publish(bus.Event{Type: "design.progress", Summary: msg})
}

// designAgent runs one prompt with no tools and returns its text and usage.
func (h *Hub) designAgent(root, prompt, model string) (string, store.Usage, error) {
	var u store.Usage
	h.mu.RLock()
	name := h.agentName
	model = firstNonEmpty(model, h.model)
	h.mu.RUnlock()
	drv, err := h.Agents.Get(name)
	if err != nil {
		return "", u, err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 6*time.Minute)
	defer cancel()
	var text strings.Builder
	byEvents := store.Usage{} // tokens as the model calls report them (Claude)
	res, err := drv.Run(ctx, agent.Request{Purpose: "design", Prompt: prompt, Dir: root, Model: model, Env: h.childEnv()}, func(ev agent.Event) {
		switch ev.Type {
		case "model.request":
			byEvents.InputTokens += toInt(ev.Data["input_tokens"])
			byEvents.CachedTokens += toInt(ev.Data["cached_tokens"])
		case "model.response":
			byEvents.OutputTokens += toInt(ev.Data["output_tokens"])
		case "model.text":
			if t, _ := ev.Data["text"].(string); t != "" {
				text.WriteString(t + "\n")
			}
		case "run.usage":
			if c, ok := ev.Data["cost_usd"].(float64); ok && c > 0 {
				u.CostUSD += c
			}
			u.PremiumRequests += toInt(ev.Data["premium_requests"])
			u.InputTokens += toInt(ev.Data["input_tokens"])
			u.OutputTokens += toInt(ev.Data["output_tokens"])
			u.CachedTokens += toInt(ev.Data["cached_tokens"])
			if t := toInt(ev.Data["total_output_tokens"]); t > 0 { // Claude: the run's real totals
				byEvents.InputTokens, byEvents.OutputTokens, byEvents.CachedTokens = toInt(ev.Data["total_input_tokens"]), t, toInt(ev.Data["total_cached_tokens"])
			}
		}
	})
	if res.CostUSD > 0 && u.CostUSD == 0 {
		u.CostUSD = res.CostUSD
	}
	if res.PremiumRequests > 0 && u.PremiumRequests == 0 {
		u.PremiumRequests = res.PremiumRequests
	}
	if u.InputTokens == 0 && u.OutputTokens == 0 { // the run's total was not reported: the calls' sum
		u.InputTokens, u.OutputTokens, u.CachedTokens = byEvents.InputTokens, byEvents.OutputTokens, byEvents.CachedTokens
	}
	u.ModelCalls++
	out := strings.TrimSpace(res.Text)
	if out == "" {
		out = strings.TrimSpace(text.String())
	}
	if err != nil {
		return out, u, err
	}
	if out == "" {
		return "", u, errors.New("the agent returned no answer")
	}
	return out, u, nil
}

func (h *Hub) recordDesignUsage(root string, u store.Usage, model string) {
	h.mu.RLock()
	name := h.agentName
	model = firstNonEmpty(model, h.model)
	h.mu.RUnlock()
	_ = h.Store.Ledger(store.LedgerEntry{At: time.Now(), RunID: "design-" + time.Now().UTC().Format("20060102T150405"), Workspace: root, Workflow: "flow-designer", Step: "design", Agent: name, Model: model, State: "done", Usage: u})
}

// SaveFlowVersion saves f as a new version of the flow baseID: a new flow
// "<name> (v<n>)" that records where it came from. The original is kept.
func (h *Hub) SaveFlowVersion(baseID string, f flow.Flow) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	base, err := flow.Load(root, baseID)
	if err != nil {
		return FlowState{}, err
	}
	family := firstNonEmpty(base.Parent, base.ID)
	rev := 2
	if list, err := flow.List(root); err == nil {
		for _, s := range list {
			if (s.ID == family || s.Parent == family) && s.Revision >= rev {
				rev = s.Revision + 1
			}
		}
	}
	name := versionSuffix.ReplaceAllString(base.Name, "")
	f.ID = flow.UniqueID(root, flow.Slug(name)+fmt.Sprintf("-v%d", rev))
	f.Name = fmt.Sprintf("%s (v%d)", name, rev)
	f.Parent, f.Revision = family, rev
	if base.Revision == 0 && base.Parent == "" { // the original becomes v1
		base.Revision = 1
		_ = flow.Save(root, base)
	}
	return h.SaveFlow(f)
}
