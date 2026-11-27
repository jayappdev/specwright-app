package core

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

func stepsOf(st FlowRunState) map[string]FlowStepState {
	m := map[string]FlowStepState{}
	for _, s := range st.Steps {
		m[s.ID] = s
	}
	return m
}

func pipeFlow() *flow.Flow {
	f := flow.New("pipe", "Pipe")
	f.Inputs = []flow.Input{{Name: "version", Type: "text"}, {Name: "suffix", Type: "text"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "v{{inputs.version}}"}},
		flow.Node{ID: "up", Type: "transform", Op: "uppercase"},
		flow.Node{ID: "tag", Type: "transform", Op: "template", Template: "[{{up.buffer_output}}]{{inputs.suffix}}"},
		flow.Node{ID: "md", Type: "output", Format: "md", Template: "Result: {{tag.buffer_output}}", Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "echo"}, {From: "echo", To: "up"}, {From: "up", To: "tag"}, {From: "tag", To: "md"}}
	return f
}

// Run again from a step: the steps before it keep their results, what the
// step receives and the flow's inputs can be changed first.
func TestRerunFromStep(t *testing.T) {
	h, _ := designHub(t)
	st, steps := runFlowTest(t, h, pipeFlow(), map[string]string{"version": "1.2", "suffix": "!"})
	if st.Status != "done" || steps["md"].Output != "Result: [V1.2]!" {
		t.Fatalf("%s %q", st.Status, steps["md"].Output)
	}
	// the plan: what tag receives, what keeps its result, what runs again
	plan, err := h.PlanRerun(st.ID, "tag")
	if err != nil || len(plan.Received) != 1 || plan.Received[0].ID != "up" || plan.Received[0].Output != "V1.2" || !plan.Received[0].Editable ||
		strings.Join(plan.Reused, ",") != "echo,up" || strings.Join(plan.Rerun, ",") != "tag,md" || plan.Values["suffix"] != "!" {
		t.Fatalf("%+v %v", plan, err)
	}
	// as it was
	r, err := h.RerunFromStep(RerunRequest{RunID: st.ID, StepID: "tag"})
	if err != nil {
		t.Fatal(err)
	}
	r = waitFlow(t, h, r.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	rs := stepsOf(r)
	if r.Status != "done" || r.Trial == nil || r.Trial.From != "tag" || strings.Join(r.Trial.Reused, ",") != "echo,up" ||
		rs["echo"].Attempts != 0 || rs["md"].Output != "Result: [V1.2]!" {
		t.Fatalf("%s %+v %q", r.Status, r.Trial, rs["md"].Output)
	}
	// what it receives, and an input it reads, changed
	r, err = h.RerunFromStep(RerunRequest{RunID: st.ID, StepID: "tag", Received: map[string]string{"up": "HAND-MADE"}, Inputs: map[string]string{"version": "9", "suffix": "?"}})
	if err != nil {
		t.Fatal(err)
	}
	r = waitFlow(t, h, r.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	rs = stepsOf(r)
	if rs["md"].Output != "Result: [HAND-MADE]?" || strings.Join(r.Trial.Edited, ",") != "up" || strings.Join(r.Trial.InputsChanged, ",") != "version,suffix" {
		t.Fatalf("%q %+v", rs["md"].Output, r.Trial)
	}
	if rs["echo"].Attempts != 0 {
		t.Fatal("a step before the restart ran again")
	}
	// only what feeds the step can be changed
	if _, err := h.RerunFromStep(RerunRequest{RunID: st.ID, StepID: "tag", Received: map[string]string{"echo": "x"}}); err == nil {
		t.Fatal("echo does not feed tag")
	}
	if _, err := h.RerunFromStep(RerunRequest{RunID: st.ID, StepID: "nope"}); err == nil {
		t.Fatal("no such step")
	}
}

// A step changed by hand is tried on a copy; changes add up, and are saved
// as a new version with the flow left as it was.
func TestStepEditAndSaveAsVersion(t *testing.T) {
	h, _ := designHub(t)
	f := pipeFlow()
	st, _ := runFlowTest(t, h, f, map[string]string{"version": "1", "suffix": ""})
	n := *f.Node("tag")
	n.Template = "<{{up.buffer_output}}>"
	tr, err := h.TryStepEdit(st.ID, n)
	if err != nil {
		t.Fatal(err)
	}
	tr = waitFlow(t, h, tr.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if stepsOf(tr)["md"].Output != "Result: <V1>" || len(tr.Trial.Steps) != 1 || tr.Trial.Steps[0].Why != "Changed by hand" {
		t.Fatalf("%q %+v", stepsOf(tr)["md"].Output, tr.Trial)
	}
	// a second change, on the trial: both
	m := *f.Node("md")
	m.Template = "Out: {{tag.buffer_output}}"
	tr2, err := h.TryStepEdit(tr.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	tr2 = waitFlow(t, h, tr2.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if stepsOf(tr2)["md"].Output != "Out: <V1>" || len(tr2.Trial.Steps) != 2 {
		t.Fatalf("%q %+v", stepsOf(tr2)["md"].Output, tr2.Trial)
	}
	bad := *f.Node("up")
	bad.Type = "ai"
	if _, err := h.TryStepEdit(st.ID, bad); err == nil {
		t.Fatal("a step's kind changed")
	}
	v, err := h.SaveTrialAsVersion(tr2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Flow.ID == "pipe" || v.Flow.Parent != "pipe" || v.Flow.Node("tag").Template != "<{{up.buffer_output}}>" {
		t.Fatalf("%+v", v.Flow)
	}
	if saved, _ := h.LoadFlow("pipe"); saved.Node("tag").Template != f.Node("tag").Template {
		t.Fatal("the flow itself changed")
	}
	if _, err := h.SaveTrialAsVersion(st.ID); err == nil {
		t.Fatal("a run with no changes was saved")
	}
}

// A new version tried on an earlier run: unchanged steps before the change
// reuse that run's results; the changed one and those after it run.
func TestTryVersionOnRun(t *testing.T) {
	h, _ := designHub(t)
	f := pipeFlow()
	st, _ := runFlowTest(t, h, f, map[string]string{"version": "3", "suffix": "."})
	v := *pipeFlow()
	v.Node("tag").Template = "({{up.buffer_output}})"
	saved, err := h.SaveFlowVersion("pipe", v)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := h.TryVersionOnRun(saved.Flow.ID, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	tr = waitFlow(t, h, tr.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	ts := stepsOf(tr)
	if tr.Status != "done" || ts["md"].Output != "Result: (V3)" || tr.FlowID != saved.Flow.ID ||
		strings.Join(tr.Trial.Changed, ",") != "tag" || strings.Join(tr.Trial.Reused, ",") != "echo,up" || tr.Trial.Against != st.ID {
		t.Fatalf("%s %q %+v", tr.Status, ts["md"].Output, tr.Trial)
	}
	if ts["echo"].Attempts != 0 {
		t.Fatal("an unchanged step ran again")
	}
	// the runs a version can be tried on: its family's, finished
	runs, err := h.VersionRuns(saved.Flow.ID)
	if err != nil || len(runs) < 2 {
		t.Fatalf("%d %v", len(runs), err)
	}
	found := false
	for _, x := range runs {
		found = found || x.ID == st.ID
	}
	if !found {
		t.Fatal("the original's run is not offered")
	}
}
