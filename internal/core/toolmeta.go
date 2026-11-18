package core

// Tool metadata and testing a tool directly.
//
//   - InspectTool: a tool's parameters as precisely as they can be known
//     without AI — a Copilot extension's own JSON Schema (read from its
//     source, approved or not), refined by extension.InferParams.
//   - RefineToolWithAI: the AI reads the tool's source and proposes its
//     parameters (types, formats, allowed values, defaults, examples), its
//     output shape and test cases. The user reviews and saves them
//     (SaveToolMeta); what runs does not change, so no new approval.
//   - RunToolWith: runs a tool (saved or a draft) with the arguments from
//     the test form, after checking them; reports the output, the time and
//     whether the output fits the declared shape.
//   - RunToolTests: runs a tool's saved test cases.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// ToolInspect is what InspectTool found.
type ToolInspect struct {
	Params  []extension.Param       `json:"params"`
	Changes []extension.ParamChange `json:"changes"`
	Source  string                  `json:"source"` // where the parameters come from, in words
	Notes   []string                `json:"notes,omitempty"`
}

// InspectTool works out a tool's parameters without AI.
func (h *Hub) InspectTool(e extension.Extension) (ToolInspect, error) {
	root, err := h.need()
	if err != nil {
		return ToolInspect{}, err
	}
	out := ToolInspect{Source: "the tool's definition"}
	base := e
	if e.Kind == extension.KindCopilot {
		if x, err := extension.CopilotExtOf(root, e); err != nil {
			out.Notes = append(out.Notes, err.Error())
		} else {
			ctx, cancel := context.WithTimeout(h.ctx, 40*time.Second)
			defs, _, err := extension.DiscoverCopilotTools(ctx, root, x, h.childEnv())
			cancel()
			if err != nil {
				out.Notes = append(out.Notes, "Could not read the extension's tools: "+err.Error())
			}
			for _, d := range defs {
				if d.Name == e.Tool {
					fresh := extension.CopilotParams(d.Parameters)
					if e.MetaAt.IsZero() || len(e.Params) == 0 { // not refined by hand: the extension's schema is the truth
						base.Params = fresh
						out.Source = "the extension's own schema (" + x.Name + "/" + x.Entry + ")"
					} else {
						out.Source = "your refined parameters (the extension's schema has " + fmt.Sprint(len(fresh)) + ")"
					}
				}
			}
		}
	}
	out.Params, out.Changes = extension.InferParams(base)
	if base.Kind == extension.KindCopilot && len(base.Params) != len(e.Params) {
		out.Changes = append([]extension.ParamChange{{Param: "*", What: fmt.Sprintf("read %d parameter(s) from the extension's schema", len(base.Params))}}, out.Changes...)
	}
	if out.Changes == nil {
		out.Changes = []extension.ParamChange{}
	}
	return out, nil
}

// ToolRefine is the AI's proposal for a tool's metadata.
type ToolRefine struct {
	extension.ToolMeta
	Why     string                  `json:"why,omitempty"`
	Changes []extension.ParamChange `json:"changes"`
	Notes   []string                `json:"notes,omitempty"`
}

// RefineToolWithAI has the AI read the tool and propose its parameters,
// output shape and test cases.
func (h *Hub) RefineToolWithAI(e extension.Extension, model string) (ToolRefine, error) {
	root, err := h.need()
	if err != nil {
		return ToolRefine{}, err
	}
	src := toolSource(root, e)
	cur, _ := json.MarshalIndent(map[string]any{"params": e.Params, "output": e.Output, "secrets": e.Secrets}, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "You document a tool so it can be called and tested reliably. Read how it is run and its source, then give its exact parameters, its output and a few test cases.\n\nTOOL: %s\nDESCRIPTION: %s\nKIND: %s\nCOMMAND: %s\n", e.Name, e.Desc, firstNonEmpty(e.Kind, "script"), e.Command)
	b.WriteString("\nWHAT IT DECLARES NOW:\n" + string(cur) + "\n")
	b.WriteString("\nSOURCE:\n" + src + "\n")
	b.WriteString(`
Give every parameter the tool really reads (from the command's {{placeholders}}, the arguments or options the source reads, the JSON it is given): its name exactly as the tool expects it, its type (string | integer | number | boolean | json | object | array), whether it is required, a short description, the values it accepts when it is a fixed set ("enum"), its format when it is a string (path | file | directory | url | date | date-time | multiline | email | password; "" otherwise), its default when the tool has one, and a realistic example. Do not invent parameters the tool does not read. Passwords, tokens and keys are secrets (environment variables), not parameters: leave them out.
Give the output's shape as JSON Schema when the tool prints JSON ({"type": "object", "properties": {...}, "required": [...]}); null when it prints text.
Give 2 or 3 test cases that work in this project as it is (real paths where a path is needed; prefer read-only cases): a name, the arguments, and what to expect ("contains": texts the output must contain; "fields": JSON paths that must exist; "fail": true for a case that must be refused).

Reply with ONLY this JSON, no explanation and no code fences:
{"params": [{"name": "...", "type": "...", "required": true, "desc": "...", "enum": [], "format": "", "default": "", "example": ""}],
 "output": <JSON Schema or null>,
 "tests": [{"name": "...", "args": {...}, "expect": {"contains": [], "fields": [], "fail": false}}],
 "why": "<one or two sentences: what you read to get this>"}`)
	h.designProgress("Reading " + e.Name + " to refine its parameters")
	text, u, err := h.designAgent(root, b.String(), model)
	h.recordDesignUsage(root, u, model)
	if err != nil {
		return ToolRefine{}, err
	}
	var got struct {
		Params []json.RawMessage    `json:"params"`
		Output json.RawMessage      `json:"output"`
		Tests  []extension.ToolTest `json:"tests"`
		Why    string               `json:"why"`
	}
	if err := json.Unmarshal([]byte(firstJSON(jsonReply(text))), &got); err != nil {
		return ToolRefine{}, fmt.Errorf("the AI's answer could not be read: %v", err)
	}
	var r ToolRefine
	r.Why = got.Why
	for _, raw := range got.Params {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		p := aiParam(m)
		if p.Name == "" {
			continue
		}
		if !inList(extension.ParamTypes, p.Type) {
			r.Notes = append(r.Notes, fmt.Sprintf("%s: type %q is not known; it is a string", p.Name, p.Type))
			p.Type = "string"
		}
		if !inList(extension.ParamFormats, p.Format) {
			p.Format = ""
		}
		r.Params = append(r.Params, p)
	}
	if len(got.Output) > 0 && string(got.Output) != "null" {
		var sc flow.Schema
		if json.Unmarshal(got.Output, &sc) == nil && sc.Known() {
			r.Output = &sc
		}
	}
	r.Tests = got.Tests
	for i := range r.Tests {
		if strings.TrimSpace(r.Tests[i].Name) == "" {
			r.Tests[i].Name = fmt.Sprintf("Case %d", i+1)
		}
		if r.Tests[i].Args == nil {
			r.Tests[i].Args = map[string]any{}
		}
	}
	r.Changes = paramChanges(e.Params, r.Params)
	if r.Output != nil && e.Output == nil {
		r.Changes = append(r.Changes, extension.ParamChange{Param: "output", What: "declares its output: " + r.Output.Compact()})
	}
	if len(r.Tests) > 0 {
		r.Changes = append(r.Changes, extension.ParamChange{Param: "tests", What: fmt.Sprintf("%d test case(s)", len(r.Tests))})
	}
	return r, nil
}

// aiParam reads one parameter as the AI wrote it (values may be any JSON).
func aiParam(m map[string]any) extension.Param {
	str := func(k string) string {
		switch v := m[k].(type) {
		case string:
			return strings.TrimSpace(v)
		case nil:
			return ""
		case float64, bool:
			return fmt.Sprint(v)
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	p := extension.Param{Name: str("name"), Type: firstNonEmpty(str("type"), "string"), Desc: firstNonEmpty(str("desc"), str("description")),
		Format: str("format"), Default: str("default"), Example: str("example")}
	if b, ok := m["required"].(bool); ok {
		p.Required = b
	}
	if l, ok := m["enum"].([]any); ok {
		for _, v := range l {
			p.Enum = append(p.Enum, fmt.Sprint(v))
		}
	}
	return p
}

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// readFileIn reads rel inside dir (no way out of it).
func readFileIn(dir, rel string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	r, err := filepath.Rel(dir, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("%s is outside %s", rel, dir)
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

// paramChanges says what differs between two parameter lists.
func paramChanges(before, after []extension.Param) []extension.ParamChange {
	old := map[string]extension.Param{}
	for _, p := range before {
		old[p.Name] = p
	}
	var out []extension.ParamChange
	for _, p := range after {
		o, ok := old[p.Name]
		if !ok {
			out = append(out, extension.ParamChange{Param: p.Name, What: "added (" + p.Type + map[bool]string{true: ", required", false: ""}[p.Required] + ")"})
			continue
		}
		delete(old, p.Name)
		var parts []string
		if o.Type != p.Type {
			parts = append(parts, "type "+o.Type+" → "+p.Type)
		}
		if o.Required != p.Required {
			parts = append(parts, map[bool]string{true: "now required", false: "now optional"}[p.Required])
		}
		if o.Format != p.Format {
			parts = append(parts, "format "+firstNonEmpty(p.Format, "none"))
		}
		if strings.Join(o.Enum, "|") != strings.Join(p.Enum, "|") && len(p.Enum) > 0 {
			parts = append(parts, "values: "+strings.Join(p.Enum, ", "))
		}
		if o.Default != p.Default && p.Default != "" {
			parts = append(parts, "default "+p.Default)
		}
		if len(parts) > 0 {
			out = append(out, extension.ParamChange{Param: p.Name, What: strings.Join(parts, "; ")})
		}
	}
	for n := range old {
		out = append(out, extension.ParamChange{Param: n, What: "removed"})
	}
	if out == nil {
		out = []extension.ParamChange{}
	}
	return out
}

// toolSource is what the AI reads of a tool: its script, or its Copilot
// extension's entry file, or the npm script's command (clipped).
func toolSource(root string, e extension.Extension) string {
	const max = 40 << 10
	clip := func(s string) string {
		if len(s) > max {
			return s[:max] + "\n…(cut)"
		}
		return s
	}
	if e.Script != "" {
		return clip(e.Script)
	}
	if e.Kind == extension.KindCopilot {
		if x, err := extension.CopilotExtOf(root, e); err == nil {
			if b, err := readFileIn(x.Dir, x.Entry); err == nil {
				return clip(b)
			}
		}
	}
	if p := e.ScriptPath; p != "" {
		if b, err := readFileIn(root, p); err == nil {
			return clip(b)
		}
	}
	return "(no source available: only the command above)"
}

// SaveToolMeta saves a tool's parameters, output shape and test cases.
func (h *Hub) SaveToolMeta(name string, m extension.ToolMeta) (extension.Extension, error) {
	if _, err := h.need(); err != nil {
		return extension.Extension{}, err
	}
	return h.exts.UpdateMetadata(name, m)
}

// ToolRun is a direct run of a tool from its test page.
type ToolRun struct {
	Args       map[string]any        `json:"args"`               // as the tool got them (checked and converted)
	Problems   []string              `json:"problems,omitempty"` // the arguments were refused: it did not run
	Command    string                `json:"command,omitempty"`
	Output     string                `json:"output"`
	Error      string                `json:"error,omitempty"`
	OK         bool                  `json:"ok"`
	DurationMS int64                 `json:"durationMs"`
	Checks     []extension.TestCheck `json:"checks"`
	Install    string                `json:"install,omitempty"` // the package install log, for a tool with packages
}

// RunToolWith runs a tool (saved or a draft) with args. Secret values given
// here are used for this run only; the others are found as usual
// (session, environment, keychain) or asked for.
func (h *Hub) RunToolWith(e extension.Extension, args map[string]any, secrets map[string]string, expect *extension.ToolExpect) (ToolRun, error) {
	root, err := h.need()
	if err != nil {
		return ToolRun{}, err
	}
	run := ToolRun{Checks: []extension.TestCheck{}}
	checked, probs := extension.CheckArgs(e, args)
	run.Args = checked
	if len(probs) > 0 {
		run.Problems = probs
		run.Checks = append(run.Checks, extension.TestCheck{Label: "The arguments fit the parameters", OK: false, Note: strings.Join(probs, "; ")})
		return run, nil
	}
	var tr extension.TestResult
	td, sp, shown, cleanup, fail, err := h.stageTool(root, e, &tr)
	if err != nil {
		return run, err
	}
	defer cleanup()
	for _, s := range tr.Steps {
		if s.K == "install" {
			run.Install = s.V
		}
	}
	if fail != nil {
		run.Error = fail.Summary
		run.Checks = append(run.Checks, extension.TestCheck{Label: "Its packages install", OK: false, Note: fail.Output})
		return run, nil
	}
	vals := map[string]string{}
	var missing []extension.Secret
	for _, s := range td.Secrets {
		if v := secrets[s.Name]; v != "" {
			vals[s.Name] = v
			h.Store.Redactor().Add(v)
		} else {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		x := td
		x.Secrets = missing
		got, err := h.ResolveSecrets(h.ctx, x)
		if err != nil {
			run.Error = "secrets: " + err.Error()
			run.Checks = append(run.Checks, extension.TestCheck{Label: "Its secrets are available", OK: false, Note: err.Error()})
			return run, nil
		}
		for k, v := range got {
			vals[k] = v
		}
	}
	if extension.HasDeps(td) && td.Kind == extension.KindCopilot {
		if err := h.Prepare(h.ctx, td); err != nil {
			run.Error = "installing its packages: " + err.Error()
			return run, nil
		}
	}
	ctx, cancel := context.WithTimeout(h.ctx, 2*time.Minute)
	defer cancel()
	start := time.Now()
	res := extension.Exec(ctx, root, td, checked, vals, h.childEnv(), 2*time.Minute)
	run.DurationMS = time.Since(start).Milliseconds()
	show := func(s string) string {
		if sp != "" {
			s = strings.ReplaceAll(s, sp, shown)
		}
		return h.Store.Redactor().String(s)
	}
	run.Command = show(res.Command)
	run.Output, run.Error, run.OK = show(res.Output), show(res.Error), res.OK
	x := extension.ToolExpect{}
	if expect != nil {
		x = *expect
	}
	run.Checks = append(run.Checks, extension.JudgeRun(e, checked, res, x)...)
	return run, nil
}

// ToolTestResult is one test case's run.
type ToolTestResult struct {
	Name string  `json:"name"`
	Pass bool    `json:"pass"`
	Run  ToolRun `json:"run"`
}

// RunToolTests runs a tool's test cases (its saved ones, or tests given).
// secrets: values typed on the test page for this session (never saved).
func (h *Hub) RunToolTests(e extension.Extension, tests []extension.ToolTest, secrets map[string]string) ([]ToolTestResult, error) {
	if tests == nil {
		tests = e.Tests
	}
	if len(tests) == 0 {
		return nil, errors.New(e.Name + " has no test cases yet: run it with arguments and save the run as one")
	}
	var out []ToolTestResult
	for _, t := range tests {
		x := t.Expect
		run, err := h.RunToolWith(e, t.Args, secrets, &x)
		if err != nil {
			return out, err
		}
		pass := len(run.Checks) > 0
		for _, c := range run.Checks {
			pass = pass && c.OK
		}
		if x.Fail && len(run.Problems) > 0 { // refused, as it should be
			pass = true
		}
		out = append(out, ToolTestResult{Name: t.Name, Pass: pass, Run: run})
	}
	return out, nil
}
