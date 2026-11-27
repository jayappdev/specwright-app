package core

// Running part of a flow again (Run flows):
//
//   - from a step: the steps before it keep the earlier run's results, the
//     step and everything after it run again; what the step receives (its
//     inputs' results) and the flow's inputs can be changed first
//     (RerunFromStep);
//   - with a step changed by hand (TryStepEdit), as the AI's step fixes
//     (flowstepfix.go) do; changes add up in a trial of a trial;
//   - a trial's changes saved as a new version of the flow
//     (SaveTrialAsVersion), the flow itself left as it was;
//   - a flow version tried on an earlier run of the flow it came from
//     (TryVersionOnRun): the steps it did not change reuse that run's
//     results, so only what changed runs again, and the two runs can be
//     compared step by step.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"codegen/internal/flow"
	"codegen/internal/store"
)

// RerunRequest asks to run a finished run again from one step.
type RerunRequest struct {
	RunID  string `json:"runId"`
	StepID string `json:"stepId"`
	// Inputs: the flow's inputs for the new run (nil: the run's own).
	// Steps before StepID keep their results either way.
	Inputs map[string]string `json:"inputs,omitempty"`
	// Received: what StepID's input steps give it instead of their result
	// in the run (step id -> output)
	Received map[string]string `json:"received,omitempty"`
}

// trialReuse: the steps of f that keep base's results: every step that runs
// before all the changed ones (not after one of them), outside loops that
// contain a changed step, done in base and with its output still readable.
func (h *Hub) trialReuse(root string, f *flow.Flow, base FlowRunState, changed map[string]bool) map[string]ReusedStep {
	li, _ := f.Loops()
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
		anc := f.Ancestors(id)
		for c := range changed {
			if anc[c] {
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
	return reuse
}

func reusedOrder(f *flow.Flow, reuse map[string]ReusedStep) []string {
	var out []string
	order, _ := f.Order()
	for _, id := range order {
		if _, ok := reuse[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// trialOptions: the options of a run that reuses base's results, with
// base's inputs and settings.
func trialOptions(f *flow.Flow, base FlowRunState, reuse map[string]ReusedStep) (FlowRunOptions, error) {
	o := FlowRunOptions{Inputs: map[string]string{}, Secrets: base.Options.Secrets, Review: "none", Model: base.Options.Model,
		TimeoutMinutes: base.Options.TimeoutMinutes, StepTimeoutSeconds: base.Options.StepTimeoutSeconds, Files: base.Options.Files, Reuse: reuse}
	for k, v := range base.Options.Inputs {
		o.Inputs[k] = v
	}
	for _, in := range f.Inputs { // secret values live only as long as the app keeps the run in memory
		if in.Type == "secret" && o.Inputs[in.Name] == flow.SecretMask && o.Secrets[in.Name] == "" {
			return o, fmt.Errorf("the run's secret input %s is no longer available (secrets are not saved): run the flow again, then try this", firstNonEmpty(in.Label, in.Name))
		}
	}
	return o, nil
}

// trialFrom starts a trial on base: its changed steps carried over.
func trialFrom(base FlowRunState) *FlowTrial {
	t := &FlowTrial{BaseRun: base.ID}
	if base.Trial != nil {
		t.BaseRun = firstNonEmpty(base.Trial.BaseRun, base.ID)
		t.Steps = append(t.Steps, base.Trial.Steps...)
	}
	return t
}

// RerunFromStep runs a finished run again from one step (see the top).
func (h *Hub) RerunFromStep(r RerunRequest) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	base, err := h.FlowRun(r.RunID)
	if err != nil {
		return FlowRunState{}, err
	}
	if base.Status == "running" || base.Status == "review" || base.Status == "scheduled" {
		return FlowRunState{}, errors.New("the run has not finished: stop it or let it finish first")
	}
	f, err := h.runFlowCopy(root, base)
	if err != nil {
		return FlowRunState{}, err
	}
	n := f.Node(r.StepID)
	if n == nil || r.StepID == "start" {
		return FlowRunState{}, fmt.Errorf("the flow has no step %s to run from", r.StepID)
	}
	trial := trialFrom(base)
	trial.From = r.StepID
	changed := map[string]bool{r.StepID: true}
	for _, ts := range trial.Steps {
		changed[ts.ID] = true
	}
	reuse := h.trialReuse(root, f, base, changed)
	// what the step receives, changed by the person
	parents := map[string]bool{}
	for _, p := range f.Parents(r.StepID) {
		parents[p] = true
	}
	var edited []string
	for id, out := range r.Received {
		if !parents[id] {
			return FlowRunState{}, fmt.Errorf("%s does not give %s its input", id, firstNonEmpty(n.Title, n.ID))
		}
		was, ok := reuse[id]
		if !ok {
			pn := f.Node(id)
			return FlowRunState{}, fmt.Errorf("the result of %s cannot be changed here (it did not finish in the run, or it is part of a loop)", firstNonEmpty(pn.Title, id))
		}
		if was.Output != out {
			reuse[id] = ReusedStep{Output: out, Branches: was.Branches}
			edited = append(edited, id)
		}
	}
	sort.Strings(edited)
	trial.Edited = edited
	trial.Reused = reusedOrder(f, reuse)
	o, err := trialOptions(f, base, reuse)
	if err != nil {
		return FlowRunState{}, err
	}
	if r.Inputs != nil {
		for _, in := range f.Inputs {
			if v, ok := r.Inputs[in.Name]; ok && in.Type != "secret" && v != o.Inputs[in.Name] {
				o.Inputs[in.Name] = v
				trial.InputsChanged = append(trial.InputsChanged, in.Name)
			}
		}
	}
	return h.startFlow(root, f, o, trial)
}

// TryStepEdit runs a trial of a step changed by hand: as an AI fix, the
// steps before it reuse the run's results.
func (h *Hub) TryStepEdit(runID string, n flow.Node) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	base, err := h.FlowRun(runID)
	if err != nil {
		return FlowRunState{}, err
	}
	f, err := h.runFlowCopy(root, base)
	if err != nil {
		return FlowRunState{}, err
	}
	was := f.Node(n.ID)
	if was == nil {
		return FlowRunState{}, fmt.Errorf("the flow has no step %s", n.ID)
	}
	if n.Type != was.Type {
		return FlowRunState{}, errors.New("a step's kind cannot change here: change it in the designer")
	}
	return h.TryStepFix(StepFix{RunID: runID, FlowID: base.FlowID, StepID: n.ID, Before: *was, Node: n, Why: "Changed by hand"})
}

// SaveTrialAsVersion saves a trial's changed steps as a new version of the
// flow; the flow itself stays as it is.
func (h *Hub) SaveTrialAsVersion(runID string) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	st, err := h.FlowRun(runID)
	if err != nil {
		return FlowState{}, err
	}
	if st.Trial == nil || len(st.Trial.Steps) == 0 {
		return FlowState{}, errors.New("this run changed no steps: there is nothing to save")
	}
	f, err := h.runFlowCopy(root, st)
	if err != nil {
		return FlowState{}, err
	}
	var names []string
	for _, ts := range st.Trial.Steps {
		names = append(names, firstNonEmpty(ts.Node.Title, ts.ID))
	}
	res, err := h.SaveFlowVersion(st.FlowID, *f)
	if err != nil {
		return res, err
	}
	h.mu.RLock()
	fr := h.flowRuns[runID]
	h.mu.RUnlock()
	if fr != nil {
		fr.update(func(s *FlowRunState) {
			s.Trial.Applied = true
			s.Trial.SavedAs = res.Flow.ID
			s.Changes = append(s.Changes, "saved "+strings.Join(names, ", ")+" as a new version")
		})
		h.flowState(fr)
	}
	return res, nil
}

// nodeSame: whether two versions of a step do the same (its place on the
// canvas aside).
func nodeSame(a, b flow.Node) bool {
	a.X, a.Y, b.X, b.Y = 0, 0, 0, 0
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// versionChanges: the steps of f that differ from the flow base ran (new,
// changed, or fed by a changed set of steps).
func versionChanges(f, was *flow.Flow) []string {
	var out []string
	for _, n := range f.Nodes {
		old := was.Node(n.ID)
		if old == nil || !nodeSame(n, *old) {
			out = append(out, n.ID)
			continue
		}
		a, b := append([]string{}, f.Parents(n.ID)...), append([]string{}, was.Parents(n.ID)...)
		sort.Strings(a)
		sort.Strings(b)
		if !reflect.DeepEqual(a, b) {
			out = append(out, n.ID)
		}
	}
	return out
}

// TryVersionOnRun runs flowID on an earlier run of another version (or of
// itself): its inputs, and the results of every step this version did not
// change and that does not come after a changed one. Compare the two runs
// to see whether the new version does better.
func (h *Hub) TryVersionOnRun(flowID, runID string) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	base, err := h.FlowRun(runID)
	if err != nil {
		return FlowRunState{}, err
	}
	f, err := flow.Load(root, flowID)
	if err != nil {
		return FlowRunState{}, err
	}
	was, err := h.runFlowCopy(root, base)
	if err != nil {
		return FlowRunState{}, fmt.Errorf("the flow of that run cannot be read: %v", err)
	}
	if err := h.checkFlowCommands(root, f); err != nil {
		return FlowRunState{}, err
	}
	diff := versionChanges(f, was)
	changed := map[string]bool{}
	for _, id := range diff {
		changed[id] = true
	}
	reuse := h.trialReuse(root, f, base, changed)
	o, err := trialOptions(f, base, reuse)
	if err != nil {
		return FlowRunState{}, err
	}
	// an input the new version added starts from its default
	for _, in := range f.Inputs {
		if _, ok := o.Inputs[in.Name]; !ok && in.Default != "" {
			o.Inputs[in.Name] = in.Default
		}
	}
	trial := &FlowTrial{BaseRun: base.ID, Against: base.ID, AgainstFlow: base.FlowID, Changed: diff, Reused: reusedOrder(f, reuse)}
	return h.startFlow(root, f, o, trial)
}

// RerunPlan is what "Run again from here" shows before it starts: what the
// step receives (editable), the flow's inputs, and which steps keep their
// results or run again.
type RerunPlan struct {
	StepID   string            `json:"stepId"`
	Title    string            `json:"title"`
	Received []RerunInput      `json:"received"` // the steps that feed it, with their result in the run
	Inputs   []flow.Input      `json:"inputs"`   // the flow's inputs (Value: the run's)
	Values   map[string]string `json:"values"`
	Reused   []string          `json:"reused"` // keep their results (titles)
	Rerun    []string          `json:"rerun"`  // run again (titles)
}

// RerunInput is one step that feeds the step to run from.
type RerunInput struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Output   string `json:"output"`
	Editable bool   `json:"editable"` // false: it runs again too (inside a loop, or it did not finish)
}

// PlanRerun says what RerunFromStep would do.
func (h *Hub) PlanRerun(runID, stepID string) (RerunPlan, error) {
	root, err := h.need()
	if err != nil {
		return RerunPlan{}, err
	}
	base, err := h.FlowRun(runID)
	if err != nil {
		return RerunPlan{}, err
	}
	f, err := h.runFlowCopy(root, base)
	if err != nil {
		return RerunPlan{}, err
	}
	n := f.Node(stepID)
	if n == nil {
		return RerunPlan{}, fmt.Errorf("the flow has no step %s", stepID)
	}
	changed := map[string]bool{stepID: true}
	if base.Trial != nil {
		for _, ts := range base.Trial.Steps {
			changed[ts.ID] = true
		}
	}
	reuse := h.trialReuse(root, f, base, changed)
	p := RerunPlan{StepID: stepID, Title: firstNonEmpty(n.Title, n.ID), Inputs: f.Inputs, Values: map[string]string{}, Received: []RerunInput{}, Reused: []string{}, Rerun: []string{}}
	if p.Inputs == nil {
		p.Inputs = []flow.Input{}
	}
	for _, in := range f.Inputs {
		if in.Type != "secret" {
			p.Values[in.Name] = base.Options.Inputs[in.Name]
		}
	}
	for _, id := range f.Parents(stepID) {
		pn := f.Node(id)
		if pn == nil || id == "start" {
			continue
		}
		r, ok := reuse[id]
		p.Received = append(p.Received, RerunInput{ID: id, Title: firstNonEmpty(pn.Title, id), Output: r.Output, Editable: ok})
	}
	order, _ := f.Order()
	for _, id := range order {
		x := f.Node(id)
		if id == "start" || x == nil {
			continue
		}
		if _, ok := reuse[id]; ok {
			p.Reused = append(p.Reused, firstNonEmpty(x.Title, id))
		} else {
			p.Rerun = append(p.Rerun, firstNonEmpty(x.Title, id))
		}
	}
	return p, nil
}

// VersionRuns lists the finished runs of a flow's family (the flow it came
// from, its other versions, itself), newest first: the runs a version can be
// tried on.
func (h *Hub) VersionRuns(flowID string) ([]store.Run, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	list, err := flow.List(root)
	if err != nil {
		return nil, err
	}
	parent := map[string]string{}
	for _, s := range list {
		if f, err := flow.Load(root, s.ID); err == nil {
			parent[f.ID] = f.Parent
		}
	}
	top := func(id string) string {
		for n := 0; n < 50 && parent[id] != ""; n++ {
			id = parent[id]
		}
		return id
	}
	fam := top(flowID)
	runs, err := h.FlowRuns(300)
	if err != nil {
		return nil, err
	}
	out := []store.Run{}
	for _, r := range runs {
		if (r.State == "done" || r.State == "failed") && top(r.Workflow) == fam {
			out = append(out, r)
		}
	}
	return out, nil
}
