package core

// Flow runs: one step — what each step type does, its prompt, inputs and
// output files.

import (
	"codegen/internal/bigout"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"codegen/internal/flow"
	"codegen/internal/store"
)

func (h *Hub) execStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, item string) (string, store.Usage, error) {
	var u store.Usage
	ctx = withFlowStep(ctx, fr.st.ID, n.ID)
	if n.Type != "improve" { // an Improve step times its AI calls and tool runs one by one (and may wait for you)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, stepTimeout(fr.st.Options, n))
		defer cancel()
	}
	f := fr.flow
	parents := f.Parents(n.ID)
	if d.Live != nil { // only the steps on the path taken
		parents = d.Live
	}
	in := ""
	for i, p := range parents {
		if i > 0 {
			in += "\n\n"
		}
		in += d.Steps[p]
	}
	d.Input = in
	switch n.Type {
	case "start":
		b, _ := json.MarshalIndent(f.MaskSecrets(d.Inputs), "", "  ") // secrets: through {{inputs.name}} only
		return string(b), u, nil
	case "tool":
		return h.toolStep(ctx, root, fr, n, d, len(parents) > 0)
	case "improve":
		return h.runImprove(ctx, root, fr, n, d, len(parents))
	case "context":
		return h.contextStep(ctx, root, fr, n, d, parents)
	case "ai":
		out, u, err := h.flowAgent(ctx, root, fr, n.ID, h.aiStepPrompt(f, n, d, len(parents)), firstNonEmpty(n.Model, fr.st.Options.Model))
		if err == nil && n.OutputSchema != nil {
			out = jsonReply(out)
		}
		return out, u, err
	case "gather":
		fr.mu.Lock()
		res := fr.gathered[n.ID]
		fr.mu.Unlock()
		out, err := flow.Gather(*n, res, d)
		return out, u, err
	case "loop":
		return "", u, errors.New("a loop runs through runLoop")
	case "merge":
		titles := map[string]string{}
		for _, p := range parents {
			if pn := f.Node(p); pn != nil && pn.Title != "" {
				titles[p] = pn.Title
			}
		}
		out, err := flow.Merge(*n, parents, d, titles)
		return out, u, err
	case "decision", "filter", "aggregate":
		if n.Source != "" {
			in = flow.Render(n.Source, d)
			d.Input = in
		}
		switch n.Type {
		case "filter":
			out, err := flow.FilterRows(*n, in, d)
			return out, u, err
		case "aggregate":
			out, err := flow.Aggregate(*n, in, d)
			return out, u, err
		}
		bs, err := flow.Decide(*n, in, d)
		if err != nil {
			return "", u, err
		}
		var labels []string
		for _, b := range bs {
			labels = append(labels, flow.BranchLabel(*n, b))
		}
		fr.update(func(st *FlowRunState) { s := fr.step(n.ID); s.Branches, s.Path = bs, strings.Join(labels, ", ") })
		h.flowPublish(fr, "flow.decision", firstNonEmpty(n.Title, n.ID)+" → "+strings.Join(labels, ", "), map[string]any{"_step": n.ID, "branches": bs})
		return in, u, nil // the data passes on along the path taken
	case "transform":
		if n.Source != "" {
			in = flow.Render(n.Source, d)
			d.Input = in
		}
		switch n.Op {
		case "ai":
			instr := flow.Render(n.Template, d)
			prompt := instr + "\n\nData:\n```\n" + in + "\n```\n\nReturn only the transformed data, with no explanation and no code fences." + h.aiSuffix(f, d)
			return h.flowAgent(ctx, root, fr, n.ID, prompt, fr.st.Options.Model)
		case "command":
			out, err := h.flowCommand(ctx, root, fr, n.ID, flow.Render(n.Expr, d), in)
			return out, u, err
		}
		out, err := flow.Transform(*n, in, d)
		return out, u, err
	case "output":
		content := in
		if strings.TrimSpace(n.Template) != "" {
			content = flow.Render(n.Template, d)
		}
		conv, err := flow.Convert(h.Store.Redactor().String(content), n.Format)
		if err != nil {
			return "", u, err
		}
		o := FlowOutputState{Node: n.ID, Title: firstNonEmpty(n.Title, n.ID), Format: n.Format, Item: item}
		o.Content, _ = clip(conv)
		d.Output = conv
		for _, a := range n.Actions {
			switch a.Type {
			case "save":
				p, err := h.saveFlowOutput(root, flow.Render(a.Path, d), conv)
				if err != nil {
					o.Errors = append(o.Errors, err.Error())
				} else {
					o.Saved = append(o.Saved, p)
					h.flowPublish(fr, "fs.write", "Saved "+p, map[string]any{"_step": n.ID, "path": p})
				}
			case "tool":
				od := d
				if p, done := h.flowInputFile(fr, n.ID+"-out", conv); p != "" {
					defer done()
					od.InputFile = p
				}
				args := map[string]any{}
				for k, v := range a.Args {
					args[k] = expandInputFile(flow.Render(v, od), od.InputFile)
				}
				_, err := h.flowTool(ctx, root, fr, n.ID, a.Tool, args, stepTimeout(fr.st.Options, n), flowInput{conv, od.InputFile})
				u.ToolCalls++
				if err != nil {
					u.ToolErrors++
					o.Errors = append(o.Errors, a.Tool+": "+err.Error())
				} else {
					o.Tools = append(o.Tools, a.Tool)
				}
			case "clipboard":
				if h.Clipboard != nil {
					if err := h.Clipboard(conv); err != nil {
						o.Errors = append(o.Errors, "copy: "+err.Error())
					} else {
						o.Copied = true
					}
				}
			case "show":
				o.Show = true
			}
		}
		fr.update(func(st *FlowRunState) { st.Outputs = append(st.Outputs, o) })
		h.flowPublish(fr, "flow.output", fmt.Sprintf("%s (%s)", o.Title, strings.ToUpper(n.Format)), map[string]any{"_step": n.ID, "node": n.ID, "format": n.Format, "saved": o.Saved})
		if len(o.Errors) > 0 {
			return conv, u, errors.New(strings.Join(o.Errors, "; "))
		}
		return conv, u, nil
	}
	return "", u, fmt.Errorf("unknown step type %q", n.Type)
}

// toolStep runs a tool step: its arguments from the templates, or built by
// the agent from the step's description; piped: steps are connected into it.
func (h *Hub) toolStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, piped bool) (string, store.Usage, error) {
	var u store.Usage
	// the previous steps' output as a file, for {{input_file}} and
	// $SPECWRIGHT_INPUT_FILE; written even when they printed nothing, so
	// the path always names a real file
	if piped {
		if p, done := h.flowInputFile(fr, n.ID, d.Input); p != "" {
			defer done()
			d.InputFile = p
		}
	}
	args := map[string]any{}
	for k, v := range n.Args {
		if r := expandInputFile(flow.Render(v, d), d.InputFile); strings.TrimSpace(r) != "" || strings.TrimSpace(n.Instruction) == "" {
			args[k] = r
		}
	}
	why := ""
	if req := strings.TrimSpace(flow.Render(n.Instruction, d)); req != "" {
		// described in words: the agent investigates and builds the rest
		actx, acancel := context.WithTimeout(ctx, 10*time.Minute)
		// the agent sees secrets masked; the fixed values that hold one go
		// back in after it answers
		red := h.Store.Redactor()
		got, w, au, err := h.aiToolArgs(actx, root, fr, n, red.String(req), redactAny(h, args), d)
		acancel()
		u.Add(au)
		if err != nil {
			return "", u, err
		}
		for k, v := range got { // the agent may refer to the file as {{input_file}}
			if s, ok := v.(string); ok {
				got[k] = expandInputFile(flow.Render(s, d), d.InputFile)
			}
		}
		for k, v := range args {
			if s, ok := v.(string); ok && red.String(s) != s {
				got[k] = v
			}
		}
		args, why = got, w
	}
	out, err := h.runToolArgs(ctx, root, fr, n, args, why, d)
	u.ToolCalls++
	if err != nil {
		u.ToolErrors++
	}
	return out, u, err
}

// runToolArgs runs a tool step's tool with args (secrets in place) and
// records them, masked, in the step's state; the values themselves are kept
// in memory for an Improve step that runs the tool again.
func (h *Hub) runToolArgs(ctx context.Context, root string, fr *flowRun, n *flow.Node, args map[string]any, why string, d flow.Data) (string, error) {
	shown := redactAny(h, args) // the step as recorded: secrets masked
	fr.update(func(st *FlowRunState) { s := fr.step(n.ID); s.Args, s.Why = shown, why })
	fr.mu.Lock()
	if fr.toolArgs == nil {
		fr.toolArgs = map[string]map[string]any{}
	}
	fr.toolArgs[n.ID] = args
	fr.mu.Unlock()
	return h.flowTool(ctx, root, fr, n.ID, n.Tool, args, toolRunTimeout(fr.st.Options), flowInput{d.Input, d.InputFile})
}

// saveFlowOutput writes an output inside the workspace.
func (h *Hub) saveFlowOutput(root, rel, content string) (string, error) {
	rel = strings.TrimSpace(rel)
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is not a path inside the project", rel)
	}
	abs := filepath.Join(root, clean)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(clean), nil
}

func (h *Hub) flowContext(f *flow.Flow) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(f.Context.Text))
	if len(f.Context.Files) > 0 {
		b.WriteString("\n\nContext files (read them): ")
		b.WriteString(strings.Join(f.Context.Files, ", "))
	}
	return strings.TrimSpace(b.String())
}

func (h *Hub) flowSkills(f *flow.Flow) string {
	var parts []string
	for _, s := range f.Skills {
		parts = append(parts, s.Path)
	}
	return strings.Join(parts, "\n")
}

// aiSuffix adds the flow's context and skills to an AI prompt.
func (h *Hub) aiSuffix(f *flow.Flow, d flow.Data) string {
	var b strings.Builder
	b.WriteString(inputFilesNote(inputFiles(h.Root(), f, d.Inputs)))
	if d.Context != "" {
		b.WriteString("\n\n## Context\n" + d.Context)
	}
	if len(f.Skills) > 0 {
		b.WriteString("\n\n## Skills (read these files and follow them)\n")
		for _, s := range f.Skills {
			fmt.Fprintf(&b, "- %s\n", s.Path)
		}
	}
	return b.String()
}

// flowTool calls a project, global or managed tool directly (as the bridge
// does): dependencies, secrets (asking the user if needed), then the run.
// flowInput is what a tool step receives from the steps connected into it.
type flowInput struct {
	text string // on standard input
	file string // the same, as a file ($SPECWRIGHT_INPUT_FILE); written by flowInputFile
}

// flowInputFile saves a step's input in the run's results folder
// (steps/<step>/input.txt) and returns its absolute path. The file is kept,
// so it can be opened after the run; done is a no-op kept for callers.
func (h *Hub) flowInputFile(fr *flowRun, step, text string) (string, func()) {
	name := "input.txt"
	if base, ok := strings.CutSuffix(step, "-out"); ok { // an output's "call a tool" action
		step, name = base, "action-input.txt"
	}
	rel, err := h.writeRunFile(stepFilesDir(fr.st.ID, step)+"/"+name, text)
	if err != nil {
		return "", func() {}
	}
	fr.update(func(st *FlowRunState) {
		if s := fr.step(step); s != nil && name == "input.txt" {
			s.InputFile = rel
		}
	})
	return filepath.Join(h.Root(), filepath.FromSlash(rel)), func() {}
}

// flowOutputFile saves a finished step's output (as it goes on to the next
// steps: redacted, and after any review edits) to steps/<step>/output.<ext>
// in the run's results folder, and returns its absolute path. It is written
// even when the output is empty, so {{<step>.output_file}} always names a
// real file.
func (h *Hub) flowOutputFile(fr *flowRun, step, out string) string {
	rel, err := h.writeRunFile(stepFilesDir(fr.st.ID, step)+"/output."+contentExt(out), out)
	if err != nil {
		return ""
	}
	fr.update(func(st *FlowRunState) {
		if s := fr.step(step); s != nil {
			s.OutputFile = rel
		}
	})
	return filepath.Join(h.Root(), filepath.FromSlash(rel))
}

var inputFileVar = regexp.MustCompile(`\$\{?SPECWRIGHT_INPUT_FILE\}?`)

// expandInputFile replaces a literal $SPECWRIGHT_INPUT_FILE typed into a
// field with the file's path: tools run without a shell, so nothing else
// would expand it.
func expandInputFile(s, path string) string {
	if path == "" || !strings.Contains(s, "SPECWRIGHT_INPUT_FILE") {
		return s
	}
	return inputFileVar.ReplaceAllLiteralString(s, path)
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	}
	return 0
}

// flowAsk describes a flow run's inputs for history.
func flowAsk(inputs map[string]string) string {
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.TrimSpace(inputs[k])
		if v == "" {
			continue
		}
		if len(v) > 300 {
			v = bigout.SafeCut(v, 300) + "…"
		}
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	return strings.TrimSpace(b.String())
}

// jsonReply takes the JSON out of an AI reply that was asked for JSON
// only: without code fences or text around it, when the JSON is there.
func jsonReply(s string) string {
	t := strings.TrimSpace(s)
	if i := strings.Index(t, "```"); i >= 0 {
		rest := t[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			t = strings.TrimSpace(rest[:j])
		}
	}
	if json.Valid([]byte(t)) {
		return t
	}
	for _, open := range []string{"{", "["} { // JSON with text around it
		i := strings.Index(t, open)
		j := strings.LastIndex(t, map[string]string{"{": "}", "[": "]"}[open])
		if i >= 0 && j > i && json.Valid([]byte(t[i:j+1])) {
			return t[i : j+1]
		}
	}
	return s
}

// aiStepPrompt is what an AI step sends: its prompt, the connected steps'
// output when the prompt does not use it, the expected reply's shape when
// one is set, and the flow's context and skills. Other shape metadata
// (tools' output shapes, the designer's field lists) is never added.
func (h *Hub) aiStepPrompt(f *flow.Flow, n *flow.Node, d flow.Data, parents int) string {
	prompt := flow.Render(n.Prompt, d)
	// connected steps' output is always part of the prompt, even when
	// the prompt does not mention it
	if in := strings.TrimSpace(d.Input); in != "" && !flow.UsesUpstream(*n, f) {
		if len(in) > 60000 {
			in = bigout.SafeCut(in, 60000) + fmt.Sprintf("\n…(shortened: %s in all)", bigout.Human(len(in)))
		}
		prompt += "\n\n## Input from the previous step" + map[bool]string{true: "s", false: ""}[parents > 1] + "\n```\n" + in + "\n```"
	}
	// an expected reply: the one place a step's shape is sent to the AI,
	// so the steps after it can rely on the fields
	if n.OutputSchema != nil && n.OutputSchema.Known() {
		prompt += "\n\nReply with only JSON in this shape, with no explanation and no code fences:\n" + n.OutputSchema.Compact()
	}
	return prompt + h.contextPacks(f, n, d, n.Prompt) + h.lessonsNote(f.ID, n.ID) + h.aiSuffix(f, d)
}
