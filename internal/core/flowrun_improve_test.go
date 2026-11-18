package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

func improveTool(t *testing.T, h *Hub, name, command string) {
	t.Helper()
	if _, err := h.SaveExtension(extension.Extension{Name: name, Desc: name, Lang: "Bash", Scope: "project", Approved: true,
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: command}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension(name); err != nil {
		t.Fatal(err)
	}
}

func improveFlow(t *testing.T, h *Hub, nodes []flow.Node, edges []flow.Edge) {
	t.Helper()
	f := flow.New("imp", "Improve")
	f.Nodes = append(f.Nodes, nodes...)
	f.Edges = edges
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

func runImp(t *testing.T, h *Hub, o FlowRunOptions) FlowRunState {
	t.Helper()
	st, err := h.RunFlow("imp", o)
	if err != nil {
		t.Fatal(err)
	}
	return waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
}

func stepOf(st FlowRunState, id string) FlowStepState {
	for _, s := range st.Steps {
		if s.ID == id {
			return s
		}
	}
	return FlowStepState{}
}

func TestImproveBeforeRerunsTheTool(t *testing.T) {
	h, _ := flowHub(t)
	// fails unless its argument is "good": the Improve step fixes the failed run
	improveTool(t, h, "need-good", `sh -c 'test "$1" = good && echo all good' sh {{v}}`)
	improveFlow(t, h, []flow.Node{
		{ID: "q", Type: "tool", Tool: "need-good", Args: map[string]string{"v": "bad"}, X: 200},
		{ID: "fix", Type: "improve", Pair: "before", Goal: "it says good", Approve: "none", X: 400},
		{ID: "o", Type: "output", Format: "txt", Template: "{{steps.q}} / {{steps.fix}}", X: 600},
	}, []flow.Edge{{From: "start", To: "q"}, {From: "q", To: "fix"}, {From: "fix", To: "o"}})
	st := runImp(t, h, FlowRunOptions{Review: "none"})
	if st.Status != "done" {
		t.Fatalf("%s: %s %+v", st.Status, st.Error, st.Steps)
	}
	q, fix := stepOf(st, "q"), stepOf(st, "fix")
	if q.Status != "done" || q.Attempts != 2 || q.Args["v"] != "good" || !strings.Contains(q.Output, "all good") {
		t.Fatalf("tool %+v", q)
	}
	if len(fix.Rounds) != 2 || fix.Rounds[0].Done || !fix.Rounds[1].Done || !strings.Contains(fix.Rounds[0].Changes, "v") {
		t.Fatalf("rounds %+v", fix.Rounds)
	}
	if !strings.Contains(fix.Output, "all good") || len(st.Outputs) != 1 || strings.Count(st.Outputs[0].Content, "all good") != 2 {
		t.Fatalf("output %q %+v", fix.Output, st.Outputs)
	}
}

func TestImproveGivesUp(t *testing.T) {
	h, _ := flowHub(t)
	improveTool(t, h, "never", `echo bad {{v}}`) // never says what the goal wants
	nodes := []flow.Node{
		{ID: "q", Type: "tool", Tool: "never", Args: map[string]string{"v": "x"}, X: 200},
		{ID: "fix", Type: "improve", Goal: "it says perfect", Rounds: 2, Approve: "none", X: 400},
	}
	edges := []flow.Edge{{From: "start", To: "q"}, {From: "q", To: "fix"}}
	improveFlow(t, h, nodes, edges)
	if st := runImp(t, h, FlowRunOptions{Review: "none"}); st.Status != "done" { // the mock finds "bad good" good enough in round 2
		t.Fatal(st.Error)
	}
	// one round only: not done after it
	nodes[1].Rounds = 1
	improveFlow(t, h, nodes, edges)
	st := runImp(t, h, FlowRunOptions{Review: "none"})
	if st.Status != "failed" || !strings.Contains(st.Error, "not done after 1 round") {
		t.Fatalf("%s %s", st.Status, st.Error)
	}
	nodes[1].ContinueOnError = true // keep the last result
	improveFlow(t, h, nodes, edges)
	st = runImp(t, h, FlowRunOptions{Review: "none"})
	if st.Status != "done" || !strings.Contains(stepOf(st, "fix").Output, "bad x") {
		t.Fatalf("%s %s %+v", st.Status, st.Error, stepOf(st, "fix"))
	}
}

func TestImproveAfterChecksTheAIResult(t *testing.T) {
	h, _ := flowHub(t)
	improveTool(t, h, "check", `sh -c 'echo "$1" | grep -q fixed && echo PASS || echo FAIL' sh {{v}}`)
	improveFlow(t, h, []flow.Node{
		{ID: "draft", Type: "improve", Pair: "after", Prompt: "Write a draft", Goal: "the check passes", Approve: "none", X: 200},
		{ID: "c", Type: "tool", Tool: "check", Args: map[string]string{"v": "{{steps.draft}}"}, X: 400},
		{ID: "o", Type: "output", Format: "txt", Template: "{{steps.draft}} | {{steps.c}}", X: 600},
	}, []flow.Edge{{From: "start", To: "draft"}, {From: "draft", To: "c"}, {From: "c", To: "o"}})
	st := runImp(t, h, FlowRunOptions{Review: "none"})
	if st.Status != "done" {
		t.Fatalf("%s: %s %+v", st.Status, st.Error, st.Steps)
	}
	d, c := stepOf(st, "draft"), stepOf(st, "c")
	if !strings.HasPrefix(d.Output, "fixed ") || len(d.Rounds) != 2 || c.Attempts != 2 || !strings.Contains(c.Output, "PASS") {
		t.Fatalf("draft %+v\ncheck %+v", d, c)
	}
	if out := st.Outputs[0].Content; !strings.Contains(out, "fixed ") || !strings.Contains(out, "PASS") {
		t.Fatalf("%q", out)
	}
}

func TestImprovePersonApproves(t *testing.T) {
	h, _ := flowHub(t)
	improveFlow(t, h, []flow.Node{
		{ID: "q", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "bad"}, X: 200},
		{ID: "fix", Type: "improve", Goal: "it says good", X: 400}, // approve: "done" by default
	}, []flow.Edge{{From: "start", To: "q"}, {From: "q", To: "fix"}})
	st, err := h.RunFlow("imp", FlowRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	if r := st.Review; r == nil || r.Type != "improve" || r.Round != 2 || r.Verdict != "done" || len(r.History) != 2 {
		t.Fatalf("%+v", st.Review)
	}
	// feedback: another round, judged with it
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "next", Prompt: "say it twice"}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" && s.Review.Round == 4 })
	var person bool
	for _, r := range st.Review.History {
		person = person || (r.By == "person" && r.Reason == "say it twice")
	}
	if !person {
		t.Fatalf("%+v", st.Review.History)
	}
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "continue"}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" || !strings.Contains(strings.Join(stepOf(st, "fix").Notes, " "), "Approved by you after round 4") {
		t.Fatalf("%s %s %+v", st.Status, st.Error, stepOf(st, "fix"))
	}
}

func TestImproveChecks(t *testing.T) {
	f := flow.New("x", "x")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "a", Type: "ai", Prompt: "hi"},
		flow.Node{ID: "fix", Type: "improve"},
		flow.Node{ID: "z", Type: "ai", Prompt: "also"})
	f.Edges = []flow.Edge{{From: "start", To: "a"}, {From: "a", To: "fix"}, {From: "a", To: "z"}}
	msgs := ""
	for _, is := range flow.Validate(f, nil) {
		msgs += is.Message + "\n"
	}
	for _, want := range []string{"Done when", "must be a tool step"} {
		if !strings.Contains(msgs, want) {
			t.Fatalf("want %q in\n%s", want, msgs)
		}
	}
}
