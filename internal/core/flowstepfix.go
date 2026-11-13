package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codegen/internal/flow"
	"codegen/internal/store"
)

// Fixing one step from Run flows: after a run, a failed step can be fixed
// (and a finished one improved) by the AI. The agent may change only that
// step's definition — same id, same connections. The change is tried on an
// unsaved copy of the flow: the steps before it reuse the earlier run's
// results, the changed step and the steps after it run again. When the
// result is right, the change is applied to the saved flow.

// StepFixRequest asks the AI to fix or improve one step of a run.
type StepFixRequest struct {
	RunID    string `json:"runId"`
	StepID   string `json:"stepId"`
	Prompt   string `json:"prompt,omitempty"`   // what is wrong or should be better
	Expected string `json:"expected,omitempty"` // what the step should give instead (an example)
	Model    string `json:"model,omitempty"`
}

// StepFix is a suggested change to one step.
type StepFix struct {
	RunID   string       `json:"runId"`
	FlowID  string       `json:"flowId"`
	StepID  string       `json:"stepId"`
	Before  flow.Node    `json:"before"`
	Node    flow.Node    `json:"node"`
	Why     string       `json:"why"`
	Changes []string     `json:"changes"`
	Issues  []flow.Issue `json:"issues"`
	Usage   store.Usage  `json:"usage"`
}

// FlowTrial is a run that tried changed steps on an unsaved copy.
type FlowTrial struct {
	BaseRun string      `json:"baseRun"` // the run whose results it reused
	Steps   []TrialStep `json:"steps"`   // the changed steps (a trial of a trial adds to them)
	Reused  []string    `json:"reused"`  // the steps it did not run again
	Applied bool        `json:"applied,omitempty"`
}

// TrialStep is one changed step.
type TrialStep struct {
	ID     string    `json:"id"`
	Before flow.Node `json:"before"`
	Node   flow.Node `json:"node"`
	Why    string    `json:"why,omitempty"`
}

// runFlowCopy is the flow as a run ran it: the saved flow, with a trial's
// changed steps.
func (h *Hub) runFlowCopy(root string, st FlowRunState) (*flow.Flow, error) {
	f, err := flow.Load(root, st.FlowID)
	if err != nil {
		return nil, err
	}
	if st.Trial != nil {
		for _, ts := range st.Trial.Steps {
			if !replaceNode(f, ts.Node) {
				return nil, fmt.Errorf("the step %s is no longer in the flow", ts.ID)
			}
		}
	}
	return f, nil
}

func replaceNode(f *flow.Flow, n flow.Node) bool {
	for i := range f.Nodes {
		if f.Nodes[i].ID == n.ID {
			n.X, n.Y = f.Nodes[i].X, f.Nodes[i].Y
			f.Nodes[i] = n
			return true
		}
	}
	return false
}

// stepOutput is a step's full output in a run (from its saved output file
// when the state holds a shortened copy).
func (h *Hub) stepOutput(root string, s FlowStepState) (string, bool) {
	if s.OutputFile != "" {
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.OutputFile))); err == nil {
			return string(b), true
		}
	}
	if !s.Truncated {
		return s.Output, s.Status == "done"
	}
	return "", false
}

// FixFlowStep asks the AI for a changed definition of one step of a run.
func (h *Hub) FixFlowStep(r StepFixRequest) (StepFix, error) {
	root, err := h.need()
	if err != nil {
		return StepFix{}, err
	}
	st, err := h.FlowRun(r.RunID)
	if err != nil {
		return StepFix{}, err
	}
	f, err := h.runFlowCopy(root, st)
	if err != nil {
		return StepFix{}, err
	}
	n := f.Node(r.StepID)
	if n == nil || n.Type == "start" {
		return StepFix{}, fmt.Errorf("no step %q to fix", r.StepID)
	}
	steps := map[string]FlowStepState{}
	for _, s := range st.Steps {
		steps[s.ID] = s
	}
	tools := h.designTools()
	fix := StepFix{RunID: r.RunID, FlowID: f.ID, StepID: n.ID, Before: *n}
	prompt := stepFixPrompt(f, n, steps, root, h, r, tools)
	h.designProgress("Reading the step and what happened in the run")
	text, u, err := h.designAgent(root, prompt, r.Model)
	fix.Usage.Add(u)
	if err != nil {
		return fix, err
	}
	known := map[string]bool{}
	for _, t := range tools {
		known[t.Name] = true
	}
	parse := func(text string) error {
		var got struct {
			Node json.RawMessage `json:"node"`
			Why  string          `json:"why"`
		}
		if err := json.Unmarshal([]byte(firstJSON(jsonReply(text))), &got); err != nil || len(got.Node) == 0 {
			return fmt.Errorf("the agent did not return a changed step (%v)", err)
		}
		var nn flow.Node
		if err := json.Unmarshal(got.Node, &nn); err != nil {
			return fmt.Errorf("the agent's step is not valid: %v", err)
		}
		nn.ID, nn.X, nn.Y = n.ID, n.X, n.Y // same step: same id, same place
		if nn.Type == "" || nn.Type == "start" {
			nn.Type = n.Type
		}
		tmp := &flow.Flow{Nodes: []flow.Node{nn}}
		fixDesignTools(tmp, known)
		fix.Node, fix.Why = tmp.Nodes[0], got.Why
		cp := cloneFlow(f)
		replaceNode(cp, fix.Node)
		fix.Issues = []flow.Issue{}
		for _, is := range h.ValidateFlow(*cp) {
			if is.Node == n.ID || is.Node == "" {
				fix.Issues = append(fix.Issues, is)
			}
		}
		return nil
	}
	if err := parse(text); err != nil {
		return fix, err
	}
	if errs := issueErrors(fix.Issues); len(errs) > 0 { // one repair round
		h.designProgress("Fixing what does not check out")
		nj, _ := json.Marshal(fix.Node)
		text, u, err := h.designAgent(root, prompt+"\n\nYour previous answer:\n"+string(nj)+"\nIt has these problems:\n- "+strings.Join(errs, "\n- ")+"\nReply again with ONLY the JSON {\"node\": {...}, \"why\": \"...\"}.", r.Model)
		fix.Usage.Add(u)
		if err == nil {
			before := fix
			if parse(text) != nil || len(issueErrors(fix.Issues)) > len(errs) {
				fix = before
			}
		}
	}
	fix.Changes = nodeChanges(fix.Before, fix.Node)
	h.recordDesignUsage(root, fix.Usage, r.Model)
	return fix, nil
}

func issueErrors(is []flow.Issue) []string {
	var out []string
	for _, i := range is {
		if i.Level == "error" {
			out = append(out, i.Message)
		}
	}
	return out
}

func cloneFlow(f *flow.Flow) *flow.Flow {
	b, _ := json.Marshal(f)
	var c flow.Flow
	_ = json.Unmarshal(b, &c)
	return &c
}

func fixDesignTools(f *flow.Flow, known map[string]bool) {
	for i := range f.Nodes {
		n := &f.Nodes[i]
		if n.Type == "tool" && !known[n.Tool] {
			switch {
			case known["builtin:"+n.Tool]:
				n.Tool = "builtin:" + n.Tool
			case known[strings.TrimPrefix(n.Tool, "builtin:")]:
				n.Tool = strings.TrimPrefix(n.Tool, "builtin:")
			}
		}
	}
}

// nodeChanges names the settings a change touches.
func nodeChanges(a, b flow.Node) []string {
	var ma, mb map[string]any
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	_ = json.Unmarshal(ja, &ma)
	_ = json.Unmarshal(jb, &mb)
	var out []string
	keys := map[string]bool{}
	for k := range ma {
		keys[k] = true
	}
	for k := range mb {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		if k != "x" && k != "y" && k != "id" {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	for _, k := range ks {
		va, _ := json.Marshal(ma[k])
		vb, _ := json.Marshal(mb[k])
		if string(va) != string(vb) {
			switch {
			case ma[k] == nil:
				out = append(out, "Adds "+k)
			case mb[k] == nil:
				out = append(out, "Removes "+k)
			default:
				out = append(out, "Changes "+k)
			}
		}
	}
	if len(out) == 0 {
		out = []string{"No changes"}
	}
	return out
}

func stepFixPrompt(f *flow.Flow, n *flow.Node, steps map[string]FlowStepState, root string, h *Hub, r StepFixRequest, tools []designTool) string {
	request := r.Prompt
	var ops []string
	for k := range flow.Ops {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	var b strings.Builder
	fmt.Fprintf(&b, "You fix ONE step of an automation flow. Change only the definition of the step %q: its settings, or its type or tool if that is what it takes. Keep its id. Do not add or remove steps or connections: the steps before it and after it stay as they are, and the steps after it must still get what they read from it.\n\n", n.ID)
	fmt.Fprintf(&b, designRules, strings.Join(ops, ", "))
	// the flow, compactly
	b.WriteString("\n\nTHE FLOW (for context):\n")
	for _, x := range f.Nodes {
		if x.Type == "start" {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s%s)%s\n", x.ID, x.Type, map[bool]string{true: " " + x.Tool, false: ""}[x.Type == "tool"], map[bool]string{true: " ← THIS STEP", false: ""}[x.ID == n.ID])
	}
	var es []string
	for _, e := range f.Edges {
		s := e.From + "→" + e.To
		if e.Branch != "" {
			s += " [" + e.Branch + "]"
		}
		es = append(es, s)
	}
	fmt.Fprintf(&b, "Connections: %s\nInputs: ", strings.Join(es, ", "))
	ij, _ := json.Marshal(f.Inputs)
	b.Write(ij)
	nj, _ := json.MarshalIndent(n, "", "  ")
	fmt.Fprintf(&b, "\n\nTHE STEP NOW:\n%s\n", nj)
	// what reached it, what it gave or its error: enough of each to see the
	// data (the start and the end of long ones) and, for JSON, its shape
	runID := ""
	for _, st := range steps {
		if st.OutputFile != "" {
			if p := strings.Index(st.OutputFile, "/steps/"); p > 0 {
				runID = filepath.Base(st.OutputFile[:p])
			}
			break
		}
	}
	show := func(label, out string, k int) {
		shape := ""
		if sh := flow.SchemaOfText(out); sh.Type == "object" || sh.Type == "array" {
			shape = "\nIts shape: " + sh.Compact()
		}
		fmt.Fprintf(&b, "\n%s (%d characters):\n```\n%s\n```%s\n", label, len(out), excerpt(out, k), shape)
	}
	items := func(id string) []string { // a step inside a loop: its per-item results
		if runID == "" {
			return nil
		}
		dir := filepath.Join(root, filepath.FromSlash(stepFilesDir(runID, id)), "items")
		ents, _ := os.ReadDir(dir)
		var out []string
		for _, e := range ents {
			if len(out) == 2 {
				break
			}
			if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				out = append(out, string(b))
			}
		}
		return out
	}
	for _, p := range f.Parents(n.ID) {
		if out, ok := h.stepOutput(root, steps[p]); ok {
			show("WHAT REACHED IT from "+p+" (the previous step's output) in the last run", out, 8000)
		} else if its := items(p); len(its) > 0 {
			for k, it := range its {
				show(fmt.Sprintf("WHAT REACHED IT from %s for loop item %d", p, k+1), it, 4000)
			}
		}
	}
	if s, ok := steps[n.ID]; ok {
		fmt.Fprintf(&b, "\nWHAT IT DID in the last run: %s", s.Status)
		if s.Error != "" {
			fmt.Fprintf(&b, " — error: %s", s.Error)
		}
		if len(s.Args) > 0 {
			aj, _ := json.Marshal(s.Args)
			fmt.Fprintf(&b, "\nIt ran with the arguments: %s", aj)
		}
		b.WriteString("\n")
		if out, ok := h.stepOutput(root, s); ok && strings.TrimSpace(out) != "" {
			show("ITS OUTPUT (what it gave; the user finds it wrong or not good enough)", out, 6000)
		} else if its := items(n.ID); len(its) > 0 {
			for k, it := range its {
				show(fmt.Sprintf("ITS OUTPUT for loop item %d", k+1), it, 3000)
			}
		}
	}
	if e := strings.TrimSpace(r.Expected); e != "" {
		fmt.Fprintf(&b, "\nWHAT IT SHOULD GIVE INSTEAD (from the user):\n```\n%s\n```\n", excerpt(e, 4000))
	}
	// how the steps after it use it
	var uses []string
	for _, x := range f.Nodes {
		if x.ID == n.ID {
			continue
		}
		texts := []string{x.Prompt, x.Template, x.Source, x.Instruction, x.Expr}
		for _, v := range x.Args {
			texts = append(texts, v)
		}
		for _, t := range texts {
			for _, v := range flow.Vars(t) {
				if strings.HasPrefix(v, n.ID+".") || strings.HasPrefix(v, "steps."+n.ID) {
					uses = append(uses, fmt.Sprintf("%s reads {{%s}}", x.ID, v))
				}
			}
		}
	}
	for _, e := range f.Edges {
		if e.From == n.ID {
			uses = append(uses, e.To+" gets its output")
		}
	}
	if len(uses) > 0 {
		fmt.Fprintf(&b, "\nTHE STEPS AFTER IT: %s\n", strings.Join(uses, "; "))
	}
	if n.Type == "tool" || strings.Contains(strings.ToLower(request), "tool") {
		b.WriteString("\nTOOLS (name: what it does; args; output):\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- %s: %s", t.Name, clipText(t.Desc, 120))
			var ps []string
			for _, p := range t.Params {
				s := p.Name
				if !p.Required {
					s += "?"
				}
				ps = append(ps, s)
			}
			if len(ps) > 0 {
				fmt.Fprintf(&b, "; args: %s", strings.Join(ps, ", "))
			}
			if t.Output != nil && t.Output.Known() {
				fmt.Fprintf(&b, "; output: %s", t.Output.Compact())
			}
			for p, m := range t.OutputBy {
				for v, sh := range m {
					fmt.Fprintf(&b, "; with %s=%q the output is JSON: %s", p, v, sh.Compact())
				}
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\nWHAT TO DO:\n%s\n", firstNonEmpty(strings.TrimSpace(request), "Fix this step so it runs and gives the steps after it what they need."))
	b.WriteString("\nReply with ONLY this JSON, no explanation and no code fences:\n{\"node\": {<the whole changed step, with the same id>}, \"why\": \"<what you changed and why, in one or two sentences>\"}")
	return b.String()
}

// TryStepFix runs a trial of a changed step: an unsaved copy of the flow,
// the steps before the changed one reusing the run's results.
func (h *Hub) TryStepFix(fix StepFix) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	base, err := h.FlowRun(fix.RunID)
	if err != nil {
		return FlowRunState{}, err
	}
	f, err := h.runFlowCopy(root, base)
	if err != nil {
		return FlowRunState{}, err
	}
	fix.Node.ID = fix.StepID
	if !replaceNode(f, fix.Node) {
		return FlowRunState{}, fmt.Errorf("the step %s is no longer in the flow", fix.StepID)
	}
	if errs := issueErrors(h.ValidateFlow(*f)); len(errs) > 0 {
		return FlowRunState{}, fmt.Errorf("the changed flow does not check out: %s", strings.Join(errs, " "))
	}
	trial := &FlowTrial{BaseRun: base.ID}
	if base.Trial != nil { // trying a change on a trial: keep its changes too
		trial.BaseRun = base.Trial.BaseRun
		for _, ts := range base.Trial.Steps {
			if ts.ID != fix.StepID {
				trial.Steps = append(trial.Steps, ts)
			}
		}
	}
	before := fix.Before
	if base.Trial != nil {
		for _, ts := range base.Trial.Steps {
			if ts.ID == fix.StepID {
				before = ts.Before // the saved flow's version
			}
		}
	}
	trial.Steps = append(trial.Steps, TrialStep{ID: fix.StepID, Before: before, Node: fix.Node, Why: fix.Why})
	// reuse: the steps that run before every changed step, outside loops
	// that contain one, with a complete result in the run
	li, _ := f.Loops()
	changed := map[string]bool{}
	for _, ts := range trial.Steps {
		changed[ts.ID] = true
	}
	steps := map[string]FlowStepState{}
	for _, s := range base.Steps {
		steps[s.ID] = s
	}
	// loops that contain a changed step run again, with their Gather
	rerun := map[string]bool{}
	for c := range changed {
		for _, l := range li.Stack[c] {
			rerun[l], rerun[li.Gather[l]] = true, true
		}
	}
	reuse := map[string]ReusedStep{}
	order, _ := f.Order()
	for _, id := range order {
		n := f.Node(id)
		if changed[id] || id == "start" || n.Type == "output" || rerun[id] || len(li.Stack[id]) > 0 {
			continue // changed, an output (cheap, and shown), or inside a loop (reused with the whole loop)
		}
		downstream := false
		for c := range changed {
			if f.Ancestors(id)[c] {
				downstream = true
			}
		}
		if downstream {
			continue
		}
		s := steps[id]
		if s.Status != "done" {
			continue
		}
		out, ok := h.stepOutput(root, s)
		if !ok {
			continue
		}
		reuse[id] = ReusedStep{Output: out, Branches: s.Branches}
	}
	// a loop is reused only together with its Gather
	for l, g := range li.Gather {
		_, lok := reuse[l]
		_, gok := reuse[g]
		if lok != gok {
			delete(reuse, l)
			delete(reuse, g)
		}
	}
	for _, id := range order {
		if _, ok := reuse[id]; ok {
			trial.Reused = append(trial.Reused, id)
		}
	}
	o := FlowRunOptions{Inputs: base.Options.Inputs, Review: "none", Model: base.Options.Model, TimeoutMinutes: base.Options.TimeoutMinutes, StepTimeoutSeconds: base.Options.StepTimeoutSeconds, Reuse: reuse}
	if o.Inputs == nil {
		o.Inputs = map[string]string{}
	}
	return h.startFlow(root, f, o, trial)
}

// ApplyStepFix writes a trial's changed steps into the saved flow.
func (h *Hub) ApplyStepFix(runID string) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	st, err := h.FlowRun(runID)
	if err != nil {
		return FlowState{}, err
	}
	if st.Trial == nil || len(st.Trial.Steps) == 0 {
		return FlowState{}, errors.New("this run is not a trial of a change")
	}
	f, err := flow.Load(root, st.FlowID)
	if err != nil {
		return FlowState{}, err
	}
	var names []string
	for _, ts := range st.Trial.Steps {
		if !replaceNode(f, ts.Node) {
			return FlowState{}, fmt.Errorf("the step %s is no longer in the flow", ts.ID)
		}
		names = append(names, firstNonEmpty(ts.Node.Title, ts.ID))
	}
	res, err := h.SaveFlow(*f)
	if err != nil {
		return res, err
	}
	h.mu.RLock()
	fr := h.flowRuns[runID]
	h.mu.RUnlock()
	if fr != nil {
		fr.update(func(s *FlowRunState) {
			s.Trial.Applied = true
			s.Changes = append(s.Changes, "changed "+strings.Join(names, ", "))
		})
		h.flowState(fr)
	}
	return res, nil
}

// excerpt is s, or for a long s its start and its end.
func excerpt(s string, k int) string {
	if len(s) <= k {
		return s
	}
	head, tail := k*2/3, k/3
	return s[:head] + fmt.Sprintf("\n…(%d characters left out)…\n", len(s)-head-tail) + s[len(s)-tail:]
}
