package core

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

func decisionFlow(t *testing.T, h *Hub) {
	t.Helper()
	f := flow.New("rel", "Release")
	f.Inputs = []flow.Input{{Name: "note", Label: "Note", Type: "text", Required: true}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "status", Type: "transform", Op: "template", Template: "Status: {{inputs.note}}", X: 200},
		flow.Node{ID: "go", Type: "decision", Title: "Go or not", Decide: "ai", Prompt: "Can the release go out today?", X: 400,
			Branches: []flow.Branch{{ID: "ship", Label: "Ship it", Hint: "everything passed"}, {ID: "hold", Label: "Hold", Hint: "something failed"}}},
		flow.Node{ID: "shipped", Type: "output", Format: "txt", Template: "shipping", X: 600, Actions: []flow.Action{{Type: "show"}}},
		flow.Node{ID: "held", Type: "output", Format: "txt", Template: "holding", X: 600, Y: 100, Actions: []flow.Action{{Type: "show"}}},
		flow.Node{ID: "other", Type: "output", Format: "txt", Template: "looking again", X: 600, Y: 200, Actions: []flow.Action{{Type: "show"}}},
	)
	f.Edges = []flow.Edge{{From: "start", To: "status"}, {From: "status", To: "go"},
		{From: "go", To: "shipped", Branch: "ship"}, {From: "go", To: "held", Branch: "hold"}, {From: "go", To: "other", Branch: flow.ElseBranch}}
	st, err := h.SaveFlow(*f)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range st.Issues {
		if is.Level == "error" {
			t.Fatal(is.Message)
		}
	}
}

func outputsOf(st FlowRunState) string {
	var o []string
	for _, x := range st.Outputs {
		o = append(o, x.Content)
	}
	return strings.Join(o, ",")
}

func stepIn(st FlowRunState, id string) FlowStepState {
	for _, s := range st.Steps {
		if s.ID == id {
			return s
		}
	}
	return FlowStepState{}
}

func TestAIDecision(t *testing.T) {
	h, _ := flowHub(t)
	decisionFlow(t, h)
	end := func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" }

	// sure: the path it chose runs
	st, _ := h.RunFlow("rel", FlowRunOptions{Inputs: map[string]string{"note": "all checks green, ship it"}, Review: "none"})
	st = waitFlow(t, h, st.ID, end)
	g := stepIn(st, "go")
	if st.Status != "done" || outputsOf(st) != "shipping" || g.Path != "Ship it" || g.Confidence != 90 || g.Why == "" {
		t.Fatalf("%s %s %q %+v", st.Status, st.Error, outputsOf(st), g)
	}

	// unsure, someone reviewing: the run asks; the person chooses another path
	st, _ = h.RunFlow("rel", FlowRunOptions{Inputs: map[string]string{"note": "[mock:unsure] ship it maybe"}})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	if st.Review == nil || len(st.Review.Choices) != 3 || st.Review.Suggested != "ship" || !strings.Contains(st.Review.Verdict, "40% sure") {
		t.Fatalf("%+v", st.Review)
	}
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "choose", Branch: "hold"}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, end)
	if st.Status != "done" || outputsOf(st) != "holding" {
		t.Fatalf("%s %q", st.Status, outputsOf(st))
	}
	ls, _ := h.FlowLessons("rel")
	if len(ls) != 1 || ls[0].Step != "go" || ls[0].Source != "decision" || !strings.Contains(ls[0].Text, `a person chose "Hold", not "Ship it"`) {
		t.Fatalf("the override is a lesson: %+v", ls)
	}
	// the next decision's prompt carries it
	f, _ := flow.Load(h.Root(), "rel")
	if note := h.lessonsNote(f.ID, "go"); !strings.Contains(note, "a person chose") {
		t.Fatal(note)
	}

	// unsure, nobody to ask: Otherwise
	st, _ = h.RunFlow("rel", FlowRunOptions{Inputs: map[string]string{"note": "[mock:unsure] hmm"}, Review: "none"})
	st = waitFlow(t, h, st.ID, end)
	if st.Status != "done" || outputsOf(st) != "looking again" || !strings.Contains(strings.Join(stepIn(st, "go").Notes, " "), "nobody to ask") {
		t.Fatalf("%s %q %+v", st.Status, outputsOf(st), stepIn(st, "go").Notes)
	}

	// an answer that is not a path: asked again, then the step fails
	st, _ = h.RunFlow("rel", FlowRunOptions{Inputs: map[string]string{"note": "[mock:bad]"}, Review: "none"})
	st = waitFlow(t, h, st.ID, end)
	if st.Status != "failed" || !strings.Contains(st.Error, "did not choose a path") || stepIn(st, "go").Usage.ModelCalls != 2 {
		t.Fatalf("%s %s %d", st.Status, st.Error, stepIn(st, "go").Usage.ModelCalls)
	}
}
