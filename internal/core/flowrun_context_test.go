package core

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestContextStep(t *testing.T) {
	h, ws := flowHub(t)
	writeFile(t, ws, "docs/billing.md", "# Billing\n\nRefunds are approved by the finance team within 2 days.\n\n"+strings.Repeat("Old notes about invoices layout. ", 80))
	writeFile(t, ws, "docs/lunch.md", "# Lunch\n\n"+strings.Repeat("The canteen opens at noon. ", 80))
	writeFile(t, ws, "package-lock.json", `{"lockfileVersion": 3}`)
	f := flow.New("ctx", "Context flow")
	f.Inputs = []flow.Input{{Name: "topic", Label: "Topic", Type: "text", Default: "refunds"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "ticket BILL-7 about refunds"}, X: 200},
		flow.Node{ID: "ctx", Type: "context", Title: "Billing context", Goal: "decide how to handle {{inputs.topic}}", X: 400, Budget: 1500,
			Sources: []flow.CtxSource{
				{Kind: "files", Paths: []string{"docs/*.md", "*.json"}, Priority: "high"},
				{Kind: "text", Text: "Policy: refunds over 500 EUR need a manager."},
				{Kind: "inputs"},
			}},
		flow.Node{ID: "shape", Type: "transform", Op: "uppercase", X: 600},
		flow.Node{ID: "answer", Type: "ai", Prompt: "Decide what to do with {{steps.shape}}", X: 800},
	)
	f.Edges = []flow.Edge{{From: "start", To: "echo"}, {From: "echo", To: "ctx"}, {From: "ctx", To: "shape"}, {From: "shape", To: "answer"}}
	if st, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	} else {
		for _, is := range st.Issues {
			if is.Level == "error" {
				t.Fatal(is.Message)
			}
		}
	}
	st, err := h.RunFlow("ctx", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatal(st.Error)
	}
	var cs FlowStepState
	for _, s := range st.Steps {
		if s.ID == "ctx" {
			cs = s
		}
	}
	rep := cs.Context
	if rep == nil || len(rep.Sources) != 4 || rep.Used > 1500 {
		t.Fatalf("%+v", rep)
	}
	if rep.Sources[0].Kind != "step" || !strings.Contains(rep.Sources[1].Note, "2 files") || !strings.Contains(rep.Sources[1].Note, "1 left out") {
		t.Fatalf("the step, then the files (the lock file left out): %+v", rep.Sources)
	}
	out := cs.Output
	for _, want := range []string{"# Context: Billing context", "Purpose: decide how to handle refunds", "[S1] echo", "### docs/billing.md", "approved by the finance team", "refunds over 500 EUR", `"topic": "refunds"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "canteen") {
		t.Fatal("a file about nothing in the purpose is left out")
	}
	// the AI step two steps on gets the pack in its prompt
	d := flow.Data{Steps: map[string]string{"ctx": out, "shape": "X"}, Inputs: map[string]string{"topic": "refunds"}}
	p := h.aiStepPrompt(f, f.Node("answer"), d, 1)
	if !strings.Contains(p, "# Context: Billing context") {
		t.Fatal(p)
	}
	// named in the prompt, or connected straight in: not added again
	f.Node("answer").Prompt = "Use {{steps.ctx}}"
	if p = h.aiStepPrompt(f, f.Node("answer"), d, 1); strings.Count(p, "# Context: Billing context") != 1 {
		t.Fatalf("named: once, from the prompt itself, not again:\n%s", p)
	}

	// condensed by the AI: one call, the sources listed
	f.Node("ctx").Condense = true
	f.Node("answer").Prompt = "Decide"
	f.Rev = ""
	h.SaveFlow(*f)
	st, _ = h.RunFlow("ctx", FlowRunOptions{Review: "none"})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	for _, s := range st.Steps {
		if s.ID == "ctx" {
			cs = s
		}
	}
	if st.Status != "done" || cs.Context == nil || !cs.Context.Condensed || !strings.Contains(cs.Output, "Sources: [S1] echo") || cs.Usage.ModelCalls == 0 {
		t.Fatalf("%s %s %+v\n%s", st.Status, st.Error, cs.Context, cs.Output)
	}
}
