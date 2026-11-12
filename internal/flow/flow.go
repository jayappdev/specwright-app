// Package flow is SpecWright's flow model: user-designed pipelines that
// string tools together. A flow has inputs (free text, files, clipboard),
// a custom context, skill files, steps (tools, AI prompts, merges and
// transforms) connected by edges, and one or more outputs, each with a
// format (md, txt, html, csv, json) and actions (save a file, call a tool,
// copy, show).
//
// Steps read data through templates: {{inputs.name}}, {{steps.<id>}} (a
// step's output), {{steps.<id>.json.path}} (a value from JSON output),
// {{context}} and {{skills}}. Edges decide the order and which steps'
// outputs are available to a step (its ancestors).
//
// Flows are stored as JSON under <workspace>/.codegen/flows/<id>.json and
// can be exported as one portable file with their skill and context files
// embedded.
package flow

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Flow is one designed flow.
type Flow struct {
	Version     int     `json:"version"`
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Inputs      []Input `json:"inputs"`
	Context     Context `json:"context"`
	Skills      []Skill `json:"skills"`
	Nodes       []Node  `json:"nodes"`
	Edges       []Edge  `json:"edges"`
	// MinutesSaved is the manual work one successful run replaces (for
	// Metrics); 0 means "estimate from the steps".
	MinutesSaved int       `json:"minutesSaved,omitempty"`
	Updated      time.Time `json:"updated,omitzero"`
}

// EstimatedMinutes is MinutesSaved, or an estimate from the steps.
func (f *Flow) EstimatedMinutes() int {
	if f.MinutesSaved > 0 {
		return f.MinutesSaved
	}
	m := 0
	for _, n := range f.Nodes {
		switch n.Type {
		case "tool":
			m += 10
		case "ai":
			m += 20
		case "merge", "transform":
			m += 5
		case "output":
			m += 5
		}
	}
	return m
}

// Input is a value the flow asks for when it runs.
type Input struct {
	Name        string   `json:"name"`  // used as {{inputs.name}}
	Label       string   `json:"label"` // shown to the user
	Type        string   `json:"type"`  // text | multiline | number | boolean | choice | file | files | clipboard
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Files       []string `json:"files,omitempty"`   // default files (workspace paths)
	Options     []string `json:"options,omitempty"` // for choice
	Accept      string   `json:"accept,omitempty"`  // for file inputs, e.g. ".pdf,image/*"
	Description string   `json:"description,omitempty"`
}

// Context is text and files every AI step and tool in the flow is given.
type Context struct {
	Text  string   `json:"text,omitempty"`
	Files []string `json:"files,omitempty"` // workspace paths
}

// Skill is a skill file the flow follows: a project or corporate library,
// or a file attached to the flow.
type Skill struct {
	Path string `json:"path"`           // workspace path of the SKILL.md / .md file
	Name string `json:"name,omitempty"` // display name
}

// Node is one step on the canvas.
type Node struct {
	ID    string  `json:"id"`
	Type  string  `json:"type"` // start | tool | ai | merge | transform | output
	Title string  `json:"title,omitempty"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Note  string  `json:"note,omitempty"`
	// Review pauses the run after this step so the user can accept,
	// iterate or stop.
	Review bool `json:"review,omitempty"`

	// tool
	Tool string            `json:"tool,omitempty"`
	Args map[string]string `json:"args,omitempty"` // parameter → template
	// Instruction is a free-form request ("the 10 customers with the most
	// orders last month"): when set, the agent investigates the project and
	// fills the arguments that Args leaves empty, then the tool runs.
	Instruction string `json:"instruction,omitempty"`

	// ai
	Prompt string `json:"prompt,omitempty"`
	Model  string `json:"model,omitempty"`

	// merge
	Mode      string `json:"mode,omitempty"`      // concat | json-object | json-array | template
	Separator string `json:"separator,omitempty"` // concat
	Template  string `json:"template,omitempty"`  // template merge, transform template, output content

	// transform
	Op      string `json:"op,omitempty"`      // see Ops
	Expr    string `json:"expr,omitempty"`    // path, pattern, …
	Replace string `json:"replace,omitempty"` // regex-replace
	Source  string `json:"source,omitempty"`  // template for the input (default: the upstream output)

	// output
	Format  string   `json:"format,omitempty"` // md | txt | html | csv | json
	Actions []Action `json:"actions,omitempty"`
}

// Action is something an output does with its content.
type Action struct {
	Type string            `json:"type"`           // save | tool | clipboard | show
	Path string            `json:"path,omitempty"` // save: workspace path template, e.g. out/{{inputs.ticket}}.md
	Tool string            `json:"tool,omitempty"` // tool: name
	Args map[string]string `json:"args,omitempty"` // tool: parameter → template ({{output}} is the content)
}

// Edge connects one step's output to another step.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// NodeTypes, transform operations, merge modes and output formats.
var (
	NodeTypes   = []string{"start", "tool", "ai", "merge", "transform", "output"}
	MergeModes  = []string{"concat", "json-object", "json-array", "template"}
	Formats     = []string{"md", "txt", "html", "csv", "json"}
	InputTypes  = []string{"text", "multiline", "number", "boolean", "choice", "file", "files", "clipboard"}
	ActionTypes = []string{"save", "tool", "clipboard", "show"}
)

// IDRE is a valid flow id (file name) and InputRE a valid input name.
var (
	IDRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)
	InputRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,39}$`)
	nodeRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,39}$`)
)

// New returns an empty flow with a start node.
func New(id, name string) *Flow {
	return &Flow{Version: 1, ID: id, Name: name, Inputs: []Input{}, Skills: []Skill{},
		Nodes: []Node{{ID: "start", Type: "start", Title: "Inputs", X: 40, Y: 120}}, Edges: []Edge{}}
}

// Slug turns a name into an id.
func Slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	if s == "" {
		s = "flow"
	}
	return s
}

// Node looks a step up by id.
func (f *Flow) Node(id string) *Node {
	for i := range f.Nodes {
		if f.Nodes[i].ID == id {
			return &f.Nodes[i]
		}
	}
	return nil
}

// Parents returns the ids of the steps that feed id, in edge order.
func (f *Flow) Parents(id string) []string {
	var out []string
	for _, e := range f.Edges {
		if e.To == id {
			out = append(out, e.From)
		}
	}
	return out
}

// Ancestors returns every step upstream of id.
func (f *Flow) Ancestors(id string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		for _, p := range f.Parents(n) {
			if !seen[p] {
				seen[p] = true
				walk(p)
			}
		}
	}
	walk(id)
	return seen
}

// Order returns the steps in run order (a topological sort, stable by
// position), or an error naming a cycle.
func (f *Flow) Order() ([]string, error) {
	in := map[string]int{}
	for _, n := range f.Nodes {
		in[n.ID] = 0
	}
	for _, e := range f.Edges {
		in[e.To]++
	}
	var ready []string
	for _, n := range f.Nodes {
		if in[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}
	pos := func(id string) (float64, float64) {
		if n := f.Node(id); n != nil {
			return n.X, n.Y
		}
		return 0, 0
	}
	var out []string
	for len(ready) > 0 {
		sort.SliceStable(ready, func(i, j int) bool {
			xi, yi := pos(ready[i])
			xj, yj := pos(ready[j])
			if xi != xj {
				return xi < xj
			}
			return yi < yj
		})
		n := ready[0]
		ready = ready[1:]
		out = append(out, n)
		for _, e := range f.Edges {
			if e.From == n {
				if in[e.To]--; in[e.To] == 0 {
					ready = append(ready, e.To)
				}
			}
		}
	}
	if len(out) != len(f.Nodes) {
		var stuck []string
		for _, n := range f.Nodes {
			if in[n.ID] > 0 {
				stuck = append(stuck, n.ID)
			}
		}
		return out, fmt.Errorf("the flow has a loop through %s", strings.Join(stuck, ", "))
	}
	return out, nil
}

// --- validation -------------------------------------------------------------

// Issue is one problem found by Validate.
type Issue struct {
	Node    string `json:"node,omitempty"` // step id, or "" for the flow
	Level   string `json:"level"`          // error | warning
	Message string `json:"message"`
}

// ToolInfo is what Validate needs to know about an available tool.
type ToolInfo struct {
	Name   string
	Params []ToolParam
}

// ToolParam is one parameter of a tool.
type ToolParam struct {
	Name     string
	Required bool
}

var varRE = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Vars lists the template variables in s.
func Vars(s string) []string {
	var out []string
	for _, m := range varRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// Validate checks a flow against the available tools. Errors stop a run;
// warnings are advice.
func Validate(f *Flow, tools []ToolInfo) []Issue {
	var is []Issue
	add := func(node, level, format string, a ...any) {
		is = append(is, Issue{Node: node, Level: level, Message: fmt.Sprintf(format, a...)})
	}
	if strings.TrimSpace(f.Name) == "" {
		add("", "error", "Give the flow a name.")
	}
	inputs := map[string]bool{}
	for _, in := range f.Inputs {
		switch {
		case !InputRE.MatchString(in.Name):
			add("start", "error", "Input %q: use letters, digits and _ (it becomes {{inputs.name}}).", in.Name)
		case inputs[in.Name]:
			add("start", "error", "Two inputs are called %q.", in.Name)
		}
		inputs[in.Name] = true
		if !contains(InputTypes, in.Type) {
			add("start", "error", "Input %q has an unknown type %q.", in.Name, in.Type)
		}
		if in.Type == "choice" && len(in.Options) == 0 {
			add("start", "error", "Input %q is a choice but has no options.", in.Name)
		}
	}
	toolByName := map[string]ToolInfo{}
	for _, t := range tools {
		toolByName[t.Name] = t
	}
	ids := map[string]bool{}
	starts, outputs := 0, 0
	for _, n := range f.Nodes {
		if !nodeRE.MatchString(n.ID) {
			add(n.ID, "error", "Step id %q is not valid.", n.ID)
		}
		if ids[n.ID] {
			add(n.ID, "error", "Two steps have the id %q.", n.ID)
		}
		ids[n.ID] = true
		switch n.Type {
		case "start":
			starts++
		case "output":
			outputs++
		}
		if !contains(NodeTypes, n.Type) {
			add(n.ID, "error", "Unknown step type %q.", n.Type)
		}
	}
	if starts != 1 {
		add("", "error", "A flow needs exactly one Inputs step.")
	}
	if outputs == 0 {
		add("", "warning", "Add an output so the flow produces something.")
	}
	for _, e := range f.Edges {
		if !ids[e.From] || !ids[e.To] {
			add("", "error", "A connection points at a step that no longer exists (%s → %s).", e.From, e.To)
		}
		if e.From == e.To {
			add(e.From, "error", "A step cannot feed itself.")
		}
		if n := f.Node(e.To); n != nil && n.Type == "start" {
			add("start", "error", "Nothing can feed the Inputs step.")
		}
		if n := f.Node(e.From); n != nil && n.Type == "output" {
			add(e.From, "error", "An output is the end of a path; it cannot feed another step.")
		}
	}
	if _, err := f.Order(); err != nil {
		add("", "error", "%s. Remove one of its connections.", strings.ToUpper(err.Error()[:1])+err.Error()[1:])
	}
	// templates may only use inputs, context, skills and upstream steps
	names, ambiguous := f.StepNames()
	checkVars := func(n Node, what, tpl string) {
		anc := f.Ancestors(n.ID)
		for _, v := range Vars(tpl) {
			head, rest, _ := strings.Cut(v, ".")
			switch head {
			case "inputs":
				name, _, _ := strings.Cut(rest, ".")
				if !inputs[name] {
					add(n.ID, "error", "%s uses {{%s}}, but there is no input called %q.", what, v, name)
				}
			case "steps":
				sid, _, _ := strings.Cut(rest, ".")
				if !ids[sid] {
					add(n.ID, "error", "%s uses {{%s}}, but there is no step %q.", what, v, sid)
				} else if !anc[sid] {
					add(n.ID, "error", "%s uses {{%s}}, but %q does not run before this step. Connect it first.", what, v, sid)
				}
			case "context", "skills", "input", "input_file", "output", "flow", "date", "now":
			default:
				if !IsStepField(rest) {
					add(n.ID, "warning", "%s uses {{%s}}, which is not a known variable.", what, v)
					break
				}
				sid := head
				if !ids[sid] {
					sid = names[head]
				}
				switch {
				case sid == "" && ambiguous[head]:
					add(n.ID, "error", "%s uses {{%s}}, but more than one step is called %q. Use the step's id instead.", what, v, head)
				case sid == "":
					add(n.ID, "error", "%s uses {{%s}}, but no step or tool is called %q.", what, v, head)
				case sid == n.ID:
					add(n.ID, "error", "%s uses {{%s}}: a step cannot read its own output.", what, v)
				case !anc[sid]:
					add(n.ID, "error", "%s uses {{%s}}, but %q does not run before this step. Connect it first.", what, v, head)
				}
			}
		}
	}
	for _, n := range f.Nodes {
		parents := f.Parents(n.ID)
		if n.Type != "start" && len(parents) == 0 {
			add(n.ID, "warning", "%s is not connected to anything before it.", label(n))
		}
		switch n.Type {
		case "tool":
			t, ok := toolByName[n.Tool]
			if n.Tool == "" {
				add(n.ID, "error", "Choose a tool for %s.", label(n))
			} else if !ok {
				add(n.ID, "error", "The tool %q is not available in this project.", n.Tool)
			} else if strings.TrimSpace(n.Instruction) == "" {
				for _, p := range t.Params {
					if p.Required && strings.TrimSpace(n.Args[p.Name]) == "" {
						add(n.ID, "error", "%s: the required parameter %q is empty (fill it in, or describe the step and let AI build it).", label(n), p.Name)
					}
				}
			}
			checkVars(n, label(n)+" › request", n.Instruction)
			for k, v := range n.Args {
				checkVars(n, label(n)+" › "+k, v)
			}
			if work := nonStart(f, parents); len(work) > 0 && strings.TrimSpace(n.Instruction) == "" && !UsesUpstream(n, f) {
				first := titles(f, work[:1])[0]
				add(n.ID, "warning", "None of %s's fields use the result of %s. It is still piped in, which only helps tools that read piped data. To use it in a field, click “← %s” next to that field, or describe the step and let AI fill the fields.",
					label(n), strings.Join(titles(f, work), " and "), first)
			}
		case "ai":
			if strings.TrimSpace(n.Prompt) == "" {
				add(n.ID, "error", "%s needs a prompt.", label(n))
			}
			checkVars(n, label(n), n.Prompt)
		case "merge":
			if !contains(MergeModes, n.Mode) {
				add(n.ID, "error", "%s: choose how to merge.", label(n))
			}
			if len(parents) < 2 {
				add(n.ID, "warning", "%s merges fewer than two steps.", label(n))
			}
			checkVars(n, label(n), n.Template)
		case "transform":
			op, ok := Ops[n.Op]
			if !ok {
				add(n.ID, "error", "%s: choose a transformation.", label(n))
			} else if op.check == nil {
			} else if err := op.check(n); err != nil {
				add(n.ID, "error", "%s: %v", label(n), err)
			}
			checkVars(n, label(n), n.Source+" "+n.Template+" "+n.Expr)
		case "output":
			if !contains(Formats, n.Format) {
				add(n.ID, "error", "%s: choose a format (md, txt, html, csv or json).", label(n))
			}
			checkVars(n, label(n), n.Template)
			for _, a := range n.Actions {
				switch a.Type {
				case "save":
					if strings.TrimSpace(a.Path) == "" {
						add(n.ID, "error", "%s: the save action needs a file path.", label(n))
					} else if p := a.Path; filepath.IsAbs(p) || strings.Contains(p, "..") {
						add(n.ID, "error", "%s: save inside the project (a relative path without ..).", label(n))
					}
					checkVars(n, label(n)+" › save", a.Path)
				case "tool":
					if _, ok := toolByName[a.Tool]; !ok {
						add(n.ID, "error", "%s: the tool %q is not available.", label(n), a.Tool)
					}
					for k, v := range a.Args {
						checkVars(n, label(n)+" › "+a.Tool+" › "+k, v)
					}
				case "clipboard", "show":
				default:
					add(n.ID, "error", "%s: unknown action %q.", label(n), a.Type)
				}
			}
		}
	}
	return is
}

// reservedVars cannot be step names in {{<step>.output_file}}.
var reservedVars = map[string]bool{"inputs": true, "steps": true, "input": true, "input_file": true, "output": true, "context": true, "skills": true, "flow": true, "date": true, "now": true}

// StepNames maps the names a template may use for a step to its id: the
// tool a step runs and its title (lowercase, words joined by -), when only
// one step has that name. ambiguous lists names shared by several steps.
func (f *Flow) StepNames() (names map[string]string, ambiguous map[string]bool) {
	names, ambiguous = map[string]string{}, map[string]bool{}
	ids := map[string]bool{}
	for _, n := range f.Nodes {
		ids[n.ID] = true
	}
	add := func(name, id string) {
		if name == "" || reservedVars[name] || ids[name] || ambiguous[name] {
			return
		}
		if prev, ok := names[name]; ok && prev != id {
			delete(names, name)
			ambiguous[name] = true
			return
		}
		names[name] = id
	}
	for _, n := range f.Nodes {
		if n.Type == "tool" {
			add(ToolRef(n.Tool), n.ID)
		}
		if strings.TrimSpace(n.Title) != "" {
			add(Slug(n.Title), n.ID)
		}
	}
	return names, ambiguous
}

// ToolRef is the name a tool step goes by in templates: the tool's name,
// without the "builtin:" of a built-in tool ({{read_file.buffer_output}}).
func ToolRef(tool string) string { return strings.TrimPrefix(tool, "builtin:") }

// UsesUpstream reports whether a step's templates read any of the steps
// connected into it ({{steps.<parent>}}) or {{input}}.
func UsesUpstream(n Node, f *Flow) bool {
	names, _ := f.StepNames()
	texts := []string{n.Prompt, n.Template, n.Source, n.Expr, n.Instruction}
	for _, v := range n.Args {
		texts = append(texts, v)
	}
	parents := map[string]bool{}
	for _, p := range f.Parents(n.ID) {
		parents[p] = true
	}
	for _, t := range texts {
		for _, v := range Vars(t) {
			head, rest, _ := strings.Cut(v, ".")
			id, _, _ := strings.Cut(rest, ".")
			if head == "input" || head == "input_file" || (head == "steps" && parents[id]) || (head == "inputs" && parents["start"]) {
				return true
			}
			if IsStepField(rest) && (parents[head] || parents[names[head]]) {
				return true
			}
		}
	}
	return false
}

func label(n Node) string {
	if n.Title != "" {
		return n.Title
	}
	return n.ID
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- storage ----------------------------------------------------------------

// Dir is where flows live in a workspace.
const Dir = ".codegen/flows"

// AssetsDir holds files that came with imported flows.
const AssetsDir = ".codegen/flows/assets"

// Path of a flow file.
func Path(root, id string) string { return filepath.Join(root, filepath.FromSlash(Dir), id+".json") }

// Summary is a flow in a list.
type Summary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Steps       int       `json:"steps"`
	Outputs     int       `json:"outputs"`
	Updated     time.Time `json:"updated"`
}

// List returns the workspace's flows, newest first.
func List(root string) ([]Summary, error) {
	ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(Dir)))
	if errors.Is(err, os.ErrNotExist) {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f, err := Load(root, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		s := Summary{ID: f.ID, Name: f.Name, Description: f.Description, Updated: f.Updated}
		for _, n := range f.Nodes {
			switch n.Type {
			case "output":
				s.Outputs++
			case "start":
			default:
				s.Steps++
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Load reads a flow.
func Load(root, id string) (*Flow, error) {
	if !IDRE.MatchString(id) {
		return nil, fmt.Errorf("no flow %q", id)
	}
	b, err := os.ReadFile(Path(root, id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no flow %q", id)
		}
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and normalizes a flow.
func Parse(b []byte) (*Flow, error) {
	var f Flow
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("not a flow file: %v", err)
	}
	f.normalize()
	return &f, nil
}

func (f *Flow) normalize() {
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Inputs == nil {
		f.Inputs = []Input{}
	}
	if f.Skills == nil {
		f.Skills = []Skill{}
	}
	if f.Edges == nil {
		f.Edges = []Edge{}
	}
	hasStart := false
	for _, n := range f.Nodes {
		if n.Type == "start" {
			hasStart = true
		}
	}
	if !hasStart {
		f.Nodes = append([]Node{{ID: "start", Type: "start", Title: "Inputs", X: 40, Y: 120}}, f.Nodes...)
	}
}

// Save writes a flow (its id must be valid).
func Save(root string, f *Flow) error {
	if !IDRE.MatchString(f.ID) {
		return fmt.Errorf("flow id %q: use lowercase letters, digits and -", f.ID)
	}
	f.normalize()
	f.Updated = time.Now().UTC().Truncate(time.Second)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	p := Path(root, f.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Delete removes a flow file (its imported assets stay).
func Delete(root, id string) error {
	if !IDRE.MatchString(id) {
		return fmt.Errorf("no flow %q", id)
	}
	return os.Remove(Path(root, id))
}

// UniqueID returns id, or id-2, id-3, … if a flow with that id exists.
func UniqueID(root, id string) string {
	cand := id
	for i := 2; ; i++ {
		if _, err := os.Stat(Path(root, cand)); errors.Is(err, os.ErrNotExist) {
			return cand
		}
		cand = fmt.Sprintf("%s-%d", id, i)
	}
}

// --- import / export --------------------------------------------------------

// Bundle is the portable export format: the flow plus the files it refers
// to (skills, context files, default input files), embedded.
type Bundle struct {
	Format   string            `json:"format"` // "specwright-flow"
	Version  int               `json:"version"`
	Exported time.Time         `json:"exported"`
	Flow     *Flow             `json:"flow"`
	Files    map[string]string `json:"files,omitempty"` // workspace path → base64 content
	Missing  []string          `json:"missing,omitempty"`
}

const bundleFormat = "specwright-flow"

// maxEmbed is the largest file embedded in an export.
const maxEmbed = 10 << 20

// Export packs a flow and its files.
func Export(root string, f *Flow) ([]byte, error) {
	b := Bundle{Format: bundleFormat, Version: 1, Exported: time.Now().UTC().Truncate(time.Second), Flow: f, Files: map[string]string{}}
	for _, p := range f.referencedFiles() {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || len(data) > maxEmbed {
			b.Missing = append(b.Missing, p)
			continue
		}
		b.Files[p] = base64.StdEncoding.EncodeToString(data)
	}
	return json.MarshalIndent(b, "", "  ")
}

func (f *Flow) referencedFiles() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = filepath.ToSlash(filepath.Clean(p))
		if p == "." || p == "" || seen[p] || filepath.IsAbs(p) || strings.HasPrefix(p, "..") {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, s := range f.Skills {
		add(s.Path)
	}
	for _, p := range f.Context.Files {
		add(p)
	}
	for _, in := range f.Inputs {
		for _, p := range in.Files {
			add(p)
		}
	}
	return out
}

// Import reads an exported bundle (or a bare flow JSON), writes its
// embedded files under .codegen/flows/assets/<id>/ (unless the same file
// already exists in the project), points the flow at them, gives it a free
// id and saves it.
func Import(root string, data []byte) (*Flow, []string, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, nil, fmt.Errorf("not a flow file: %v", err)
	}
	if b.Format != bundleFormat || b.Flow == nil {
		f, err := Parse(data) // a bare flow
		if err != nil || len(f.Nodes) == 0 || f.Name == "" {
			return nil, nil, errors.New("not a SpecWright flow (export one from the Flow designer)")
		}
		b = Bundle{Flow: f}
	}
	f := b.Flow
	f.normalize()
	if f.Name == "" {
		f.Name = "Imported flow"
	}
	if !IDRE.MatchString(f.ID) {
		f.ID = Slug(f.Name)
	}
	f.ID = UniqueID(root, f.ID)
	var notes []string
	moved := map[string]string{}
	for p, enc := range b.Files {
		data, err := base64.StdEncoding.DecodeString(enc)
		clean := filepath.ToSlash(filepath.Clean(p))
		if err != nil || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			notes = append(notes, "skipped "+p)
			continue
		}
		if cur, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(clean))); err == nil && string(cur) == string(data) {
			continue // already here, unchanged
		}
		dst := AssetsDir + "/" + f.ID + "/" + clean
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(dst))), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(dst)), data, 0o644); err != nil {
			return nil, nil, err
		}
		moved[clean] = dst
	}
	re := func(p string) string {
		if m, ok := moved[filepath.ToSlash(filepath.Clean(p))]; ok {
			return m
		}
		return p
	}
	for i := range f.Skills {
		f.Skills[i].Path = re(f.Skills[i].Path)
	}
	for i := range f.Context.Files {
		f.Context.Files[i] = re(f.Context.Files[i])
	}
	for i := range f.Inputs {
		for j := range f.Inputs[i].Files {
			f.Inputs[i].Files[j] = re(f.Inputs[i].Files[j])
		}
	}
	if len(moved) > 0 {
		notes = append(notes, fmt.Sprintf("%d file(s) added under %s/%s/", len(moved), AssetsDir, f.ID))
	}
	for _, m := range b.Missing {
		notes = append(notes, "not included in the export: "+m)
	}
	if err := Save(root, f); err != nil {
		return nil, nil, err
	}
	return f, notes, nil
}

func nonStart(f *Flow, ids []string) []string {
	var out []string
	for _, id := range ids {
		if n := f.Node(id); n != nil && n.Type != "start" {
			out = append(out, id)
		}
	}
	return out
}

func titles(f *Flow, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n := f.Node(id); n != nil {
			out = append(out, label(*n))
		}
	}
	return out
}
