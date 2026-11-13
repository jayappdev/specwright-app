package core

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

// A failed step is fixed by the AI, tried on a copy (the steps before it
// reused, not run again) and applied to the saved flow.
func TestFixStepTryAndApply(t *testing.T) {
	h, s := designHub(t)
	f := flow.New("pipe", "Pipe")
	f.Inputs = []flow.Input{{Name: "version", Type: "text"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "fixed {{inputs.version}}"}},
		flow.Node{ID: "up", Type: "transform", Op: "uppercase"},
		flow.Node{ID: "bad", Type: "transform", Op: "json-path", Expr: "x"}, // fails: the input is not JSON
		flow.Node{ID: "md", Type: "output", Format: "md", Template: "Result: {{bad.buffer_output}}", Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "echo"}, {From: "echo", To: "up"}, {From: "up", To: "bad"}, {From: "bad", To: "md"}}
	st, steps := runFlowTest(t, h, f, map[string]string{"version": "1.2"})
	if st.Status != "failed" || steps["bad"].Status != "failed" {
		t.Fatalf("%s %+v", st.Status, steps["bad"])
	}
	s.replies = []string{"```json\n" + `{"node": {"id": "renamed", "type": "transform", "title": "Shout", "op": "template", "template": "OK {{up.buffer_output}}"}, "why": "The input is text, not JSON."}` + "\n```"}
	fix, err := h.FixFlowStep(StepFixRequest{RunID: st.ID, StepID: "bad", Prompt: "it fails"})
	if err != nil {
		t.Fatal(err)
	}
	p := s.prompts[len(s.prompts)-1]
	for _, want := range []string{`Change only the definition of the step "bad"`, "WHAT REACHED IT from up in the last run:\n```\nFIXED 1.2", "WHAT IT DID in the last run: failed", "md reads {{bad.buffer_output}}", "WHAT TO DO:\nit fails"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Contains(p, "TOOLS (name:") {
		t.Error("no tool catalogue for a transform step")
	}
	if fix.Node.ID != "bad" || fix.Node.Op != "template" || len(issueErrors(fix.Issues)) != 0 || !strings.Contains(strings.Join(fix.Changes, ","), "Changes op") {
		t.Fatalf("%+v", fix)
	}
	trial, err := h.TryStepFix(fix)
	if err != nil {
		t.Fatal(err)
	}
	trial = waitFlow(t, h, trial.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if trial.Status != "done" || trial.Trial == nil || strings.Join(trial.Trial.Reused, ",") != "echo,up" {
		t.Fatalf("%s %s %+v", trial.Status, trial.Error, trial.Trial)
	}
	ts := map[string]FlowStepState{}
	for _, x := range trial.Steps {
		ts[x.ID] = x
	}
	if ts["echo"].Attempts != 0 || len(ts["echo"].Notes) == 0 || ts["md"].Output != "Result: OK FIXED 1.2" {
		t.Fatalf("echo %+v md %q", ts["echo"], ts["md"].Output)
	}
	if saved, _ := h.LoadFlow("pipe"); saved.Node("bad").Op != "json-path" {
		t.Fatal("nothing is saved before Apply")
	}
	if _, err := h.ApplyStepFix(trial.ID); err != nil {
		t.Fatal(err)
	}
	saved, _ := h.LoadFlow("pipe")
	if n := saved.Node("bad"); n.Op != "template" || n.Title != "Shout" || len(saved.Nodes) != 5 {
		t.Fatalf("%+v", saved.Nodes)
	}
	if _, err := h.ApplyStepFix(st.ID); err == nil {
		t.Fatal("only a trial can be applied")
	}
}
